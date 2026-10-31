package isureal

import (
	"encoding/hex"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// APIError は API のエラー応答。
type APIError struct {
	Status int
	Code   string
}

func (e *APIError) Error() string { return e.Code }

func NewAPIError(status int, code string) error { return &APIError{Status: status, Code: code} }

var usernamePattern = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)

func validUserText(value string, minimum, maximum int) bool {
	if !utf8.ValidString(value) {
		return false
	}
	length := utf8.RuneCountInString(value)
	return length >= minimum && length <= maximum
}

func validRegion(region string) bool {
	switch region {
	case "america", "europe", "asia":
		return true
	}
	return false
}

func normalizeSearchText(value string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(value)))
}

type Relation struct {
	ID        ID
	UpdatedAt int64
	A, B      *User
}

func (r *Relation) other(u *User) *User {
	if r.A == u {
		return r.B
	}
	return r.A
}

type FriendRequest struct {
	ID        ID
	Requester *User
	Recipient *User
	Status    string
	UpdatedAt int64
}

type DayStat struct {
	Active  int32
	Deleted int32
}

type UserSession struct {
	Hash [32]byte
	T    int64
}

type User struct {
	ID            ID
	IDStr         string
	Idx           int32 // 初期ユーザーの番号。新しいユーザーは -1
	Username      string
	SecretHash    string
	Key           [32]byte
	DisplayName   string
	Bio           string
	HasProfile    bool
	ProfileImg    ID
	ProfileSig    [32]byte
	ProfileSigExp int64
	Region        string
	Certified     bool
	Active        bool
	FoldUser      string
	FoldName      string

	Friends   map[*User]*Relation
	Following map[*User]*Relation
	Followers map[*User]*Relation
	Blocking  map[*User]*Relation // 自分がブロックしている相手
	BlockedBy map[*User]*Relation // 自分をブロックしている相手
	Incoming  map[*User]*FriendRequest
	Outgoing  map[*User]*FriendRequest

	Days        map[int32]*DayStat // この期間に作られた投稿の日付ごとの件数
	Posts       []*Post            // この期間に作られた投稿（古い順）
	ActivePosts []*Post            // 期間中で削除されていない投稿
	Session     *UserSession
	SSE         map[*sseSub]struct{}
}

func (u *User) accountType() string {
	if u.Certified {
		return "certified_public"
	}
	return "regular"
}

func (u *User) status() string {
	if u.Active {
		return "active"
	}
	return "deleted"
}

func (u *User) isBlockedWith(o *User) bool {
	if _, ok := u.Blocking[o]; ok {
		return true
	}
	_, ok := u.BlockedBy[o]
	return ok
}

type Window struct {
	ID             ID
	IDStr          string
	Region         string
	LocalDate      int32
	LocalDateStr   string
	StartsAt       int64
	OnTimeDeadline int64
	Ended          bool
	Posts          []*Post
}

type PostLoc struct {
	Kind      uint8
	HasCoords bool
	Lat, Lon  float64
	Locality  string
}

type Post struct {
	ID          ID
	IDStr       string
	Author      *User
	Window      *Window
	ImageID     ID
	ImageSig    [32]byte
	ImageSigExp int64
	Caption     string
	Vis         uint8
	Loc         PostLoc
	PostedAt    int64
	Deleted     bool
	Realmojis   []*Realmoji // 古い順
	Comments    []*Comment  // 古い順
}

type Realmoji struct {
	ID          ID
	IDStr       string
	Post        *Post
	Reactor     *User
	ImageID     ID
	ImageSig    [32]byte
	ImageSigExp int64
	Type        uint8
	CreatedAt   int64
	Replaced    bool
	Deleted     bool
}

type Comment struct {
	ID        ID
	IDStr     string
	Post      *Post
	Author    *User
	Body      string
	CreatedAt int64
	Deleted   bool
}

// LiveImage はこの期間に作られた画像。本体は Origin のノードのファイルにある。
type LiveImage struct {
	ID     ID
	Owner  *User
	Kind   uint8
	Post   *Post
	RM     *Realmoji
	Len    uint32
	CRC    uint32
	Origin uint8
	Sig    [32]byte
	SigExp int64
}

type Admin struct {
	ID           ID
	IDStr        string
	Username     string
	PasswordHash string
}

type sseSub struct {
	user *User
	ch   chan *Window
	gone chan struct{}
}

// State はアプリの状態すべて。読みは RLock、順序付けされたコマンドの適用だけが Lock を取る。
type State struct {
	mu   sync.RWMutex
	pack *Pack

	users       map[ID]*User
	byName      map[string]*User
	sortedUsers []*User
	initUsers   []*User

	sessions      map[[32]byte]*User
	adminSessions map[[32]byte]*Admin
	admins        map[string]*Admin

	requests   map[ID]*FriendRequest
	initWindow []*Window
	windows    map[ID]*Window
	current    map[string]*Window
	maxDate    map[string]int32

	posts     map[ID]*Post
	comments  map[ID]*Comment
	realmojis map[ID]*Realmoji
	images    map[ID]*LiveImage

	histPostDeleted    map[uint32]bool
	histCommentDeleted map[uint32]bool
	histRMDeleted      map[uint32]bool

	sseByRegion map[string]map[*sseSub]struct{}
	lastTs      int64

	bans atomic.Pointer[map[string]int64]

	onWindowOpen func(w *Window, subs []*sseSub)
}

func NewState(pack *Pack) *State {
	s := &State{pack: pack}
	s.reset()
	return s
}

func (s *State) newUserFromInit(u *InitUser, idx int32) *User {
	id := mustID(u.ID)
	nu := &User{
		ID: id, IDStr: u.ID, Idx: idx, Username: u.Username, SecretHash: u.SecretKeyHash,
		DisplayName: u.DisplayName, Bio: u.Bio, Region: u.Region,
		Certified: u.AccountType == "certified_public", Active: u.Status == "active",
	}
	key, _ := hex.DecodeString(u.SecretKeyHash)
	copy(nu.Key[:], key)
	if u.ProfileImageID != nil {
		nu.HasProfile = true
		nu.ProfileImg = mustID(*u.ProfileImageID)
		nu.ProfileSig = s.pack.UserSigs[idx]
		nu.ProfileSigExp = s.pack.SigExp
	}
	return nu
}

var initFoldCache sync.Map // ユーザー名と表示名の正規化は重いので、初期ユーザーの分は覚えておく

func foldOf(v string) string {
	if c, ok := initFoldCache.Load(v); ok {
		return c.(string)
	}
	f := normalizeSearchText(v)
	initFoldCache.Store(v, f)
	return f
}

// reset は状態を初期データへ戻す。呼ぶ側が Lock を持つ（起動時を除く）。
func (s *State) reset() {
	init := s.pack.Init
	s.users = make(map[ID]*User, len(init.Users)+2048)
	s.byName = make(map[string]*User, len(init.Users)+2048)
	s.initUsers = make([]*User, len(init.Users))
	s.sortedUsers = make([]*User, 0, len(init.Users)+2048)
	for i := range init.Users {
		u := s.newUserFromInit(&init.Users[i], int32(i))
		u.FoldUser = foldOf(u.Username)
		u.FoldName = foldOf(u.DisplayName)
		s.users[u.ID] = u
		s.byName[u.Username] = u
		s.initUsers[i] = u
		s.sortedUsers = append(s.sortedUsers, u)
	}
	sort.Slice(s.sortedUsers, func(i, j int) bool {
		a, b := s.sortedUsers[i], s.sortedUsers[j]
		if a.Username != b.Username {
			return a.Username < b.Username
		}
		return a.IDStr < b.IDStr
	})
	for i := range init.Friendships {
		f := &init.Friendships[i]
		a, b := s.users[mustID(f.Low)], s.users[mustID(f.High)]
		r := &Relation{ID: mustID(f.ID), UpdatedAt: f.UpdatedAt.UnixMicro(), A: a, B: b}
		addRel(&a.Friends, b, r)
		addRel(&b.Friends, a, r)
	}
	for i := range init.Follows {
		f := &init.Follows[i]
		a, b := s.users[mustID(f.Follower)], s.users[mustID(f.Followee)]
		r := &Relation{ID: mustID(f.ID), UpdatedAt: f.UpdatedAt.UnixMicro(), A: a, B: b}
		addRel(&a.Following, b, r)
		addRel(&b.Followers, a, r)
	}
	for i := range init.Blocks {
		f := &init.Blocks[i]
		a, b := s.users[mustID(f.Blocker)], s.users[mustID(f.Blocked)]
		r := &Relation{ID: mustID(f.ID), A: a, B: b}
		addRel(&a.Blocking, b, r)
		addRel(&b.BlockedBy, a, r)
	}
	s.requests = make(map[ID]*FriendRequest, len(init.FriendRequests)+4096)
	for i := range init.FriendRequests {
		f := &init.FriendRequests[i]
		a, b := s.users[mustID(f.Requester)], s.users[mustID(f.Recipient)]
		r := &FriendRequest{ID: mustID(f.ID), Requester: a, Recipient: b, Status: f.Status, UpdatedAt: f.UpdatedAt.UnixMicro()}
		s.requests[r.ID] = r
		if r.Status == "pending" {
			addReq(&a.Outgoing, b, r)
			addReq(&b.Incoming, a, r)
		}
	}
	s.windows = make(map[ID]*Window, len(init.Windows)+64)
	s.initWindow = make([]*Window, len(init.Windows))
	s.current = map[string]*Window{}
	s.maxDate = map[string]int32{}
	for i := range init.Windows {
		w := &init.Windows[i]
		nw := &Window{
			ID: mustID(w.ID), IDStr: w.ID, Region: w.Region, LocalDate: w.LocalDate,
			LocalDateStr: daysToDate(w.LocalDate).Format("2006-01-02"),
			StartsAt:     w.StartsAt.UnixMicro(), OnTimeDeadline: w.OnTimeDeadline.UnixMicro(),
			Ended: w.EndedAt != nil,
		}
		s.windows[nw.ID] = nw
		s.initWindow[i] = nw
		if !nw.Ended {
			s.current[nw.Region] = nw
		}
		if nw.LocalDate > s.maxDate[nw.Region] {
			s.maxDate[nw.Region] = nw.LocalDate
		}
	}
	s.admins = map[string]*Admin{}
	for i := range init.Admins {
		a := &init.Admins[i]
		s.admins[a.Username] = &Admin{ID: mustID(a.ID), IDStr: a.ID, Username: a.Username, PasswordHash: a.PasswordHash}
	}
	s.sessions = map[[32]byte]*User{}
	s.adminSessions = map[[32]byte]*Admin{}
	s.posts = map[ID]*Post{}
	s.comments = map[ID]*Comment{}
	s.realmojis = map[ID]*Realmoji{}
	s.images = map[ID]*LiveImage{}
	s.histPostDeleted = map[uint32]bool{}
	s.histCommentDeleted = map[uint32]bool{}
	s.histRMDeleted = map[uint32]bool{}
	for _, subs := range s.sseByRegion {
		for sub := range subs {
			close(sub.gone)
		}
	}
	s.sseByRegion = map[string]map[*sseSub]struct{}{}
	empty := map[string]int64{}
	s.bans.Store(&empty)
	s.lastTs = 0
}

func addRel(m *map[*User]*Relation, k *User, r *Relation) {
	if *m == nil {
		*m = make(map[*User]*Relation, 4)
	}
	(*m)[k] = r
}

func addReq(m *map[*User]*FriendRequest, k *User, r *FriendRequest) {
	if *m == nil {
		*m = make(map[*User]*FriendRequest, 4)
	}
	(*m)[k] = r
}

func (s *State) insertSorted(u *User) {
	i := sort.Search(len(s.sortedUsers), func(i int) bool {
		a := s.sortedUsers[i]
		if a.Username != u.Username {
			return a.Username > u.Username
		}
		return a.IDStr >= u.IDStr
	})
	s.sortedUsers = append(s.sortedUsers, nil)
	copy(s.sortedUsers[i+1:], s.sortedUsers[i:])
	s.sortedUsers[i] = u
}

// viewer 周りの判定

func (s *State) hasPostedToday(u *User) bool {
	w := s.current[u.Region]
	if w == nil {
		return false
	}
	d := u.Days[w.LocalDate]
	return d != nil && d.Active > 0
}

func isFriendOfFriend(viewer, author *User) bool {
	// 共通の友達がいるか。小さいほうを回す。
	a, b := viewer.Friends, author.Friends
	if len(a) > len(b) {
		a, b = b, a
	}
	for f := range a {
		if _, ok := b[f]; ok {
			return true
		}
	}
	return false
}

func (s *State) canViewLive(viewer *User, p *Post, posted bool) bool {
	author := p.Author
	if author == viewer {
		return true
	}
	if p.Window.Ended || !posted || viewer.isBlockedWith(author) {
		return false
	}
	switch p.Vis {
	case PostVisPublic:
		return true
	case PostVisFriends:
		_, ok := viewer.Friends[author]
		return ok
	case PostVisFoF:
		if _, ok := viewer.Friends[author]; ok {
			return true
		}
		return isFriendOfFriend(viewer, author)
	}
	return false
}

func (s *State) canCommentLive(viewer *User, p *Post) bool {
	if p.Deleted || !p.Author.Active || !viewer.Active {
		return false
	}
	posted := s.hasPostedToday(viewer)
	if !s.canViewLive(viewer, p, posted) || p.Window.Ended {
		return false
	}
	if viewer == p.Author {
		return posted
	}
	if p.Vis == PostVisPublic {
		return true
	}
	_, ok := viewer.Friends[p.Author]
	return ok
}

// histPost は過去の投稿を探す（削除済みと著者が退会済みのものは除く）。
func (s *State) histPost(id ID) (*HistPost, uint32) {
	idx, ok := findIndex(s.pack.PostIDs, id)
	if !ok {
		return nil, 0
	}
	p := &s.pack.Posts[idx]
	if p.DeletedInPack() || s.histPostDeleted[idx] {
		return nil, 0
	}
	return p, idx
}

func trimAPIPrefix(s string) string { return strings.TrimPrefix(s, "/api/") }

var _ = http.StatusOK
