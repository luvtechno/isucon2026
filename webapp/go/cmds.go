package isureal

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

// Cmd は状態を変える操作。順序付け役が検査して適用し、受理したものだけを同じ順序で全ノードへ配る。
// 各ノードは同じ順序で同じ apply を実行するので、同じ状態になる。ID と時刻はコマンドの中に入っている。
type CmdKind uint8

const (
	CmdRegister CmdKind = iota + 1
	CmdLogin
	CmdLogout
	CmdUpdateMe
	CmdSetProfileImage
	CmdDeleteProfileImage
	CmdDeleteAccount
	CmdCreatePost
	CmdUpdatePost
	CmdDeletePost
	CmdCreateComment
	CmdDeleteComment
	CmdReplaceRealmoji
	CmdDeleteRealmoji
	CmdFriendRequest
	CmdTransitionRequest
	CmdDeleteFriendship
	CmdFollow
	CmdUnfollow
	CmdBlock
	CmdUnblock
	CmdAdminLogin
	CmdAdminLogout
	CmdCertify
	CmdOpenWindow
	CmdBan
)

type Cmd struct {
	Kind      CmdKind      `json:"k"`
	Origin    uint8        `json:"o,omitzero"`
	Req       uint64       `json:"r,omitzero"`
	User      ID           `json:"u,omitzero"`
	Target    ID           `json:"x,omitzero"`
	Obj       ID           `json:"i,omitzero"`
	Obj2      ID           `json:"j,omitzero"`
	Img       ID           `json:"m,omitzero"`
	ImgLen    uint32       `json:"l,omitzero"`
	ImgCRC    uint32       `json:"c,omitzero"`
	ImgOrigin uint8        `json:"g,omitzero"`
	ImgSig    string       `json:"y,omitzero"`
	ImgExp    int64        `json:"w,omitzero"`
	Token     string       `json:"h,omitzero"`
	T         int64        `json:"t,omitzero"`
	Str1      string       `json:"a,omitzero"`
	Str2      string       `json:"b,omitzero"`
	Str3      string       `json:"d,omitzero"`
	P1        *string      `json:"p,omitzero"`
	P2        *string      `json:"q,omitzero"`
	P3        *string      `json:"s,omitzero"`
	Vis       uint8        `json:"v,omitzero"`
	Loc       *PostLocJSON `json:"z,omitzero"`
	Date      int32        `json:"e,omitzero"`
	Secs      int64        `json:"n,omitzero"`
}

type PostLocJSON struct {
	Kind      uint8   `json:"k"`
	HasCoords bool    `json:"h,omitzero"`
	Lat       float64 `json:"a,omitzero"`
	Lon       float64 `json:"o,omitzero"`
	Locality  string  `json:"l,omitzero"`
}

func (id ID) MarshalText() ([]byte, error) { return id.appendTo(make([]byte, 0, 36)), nil }
func (id *ID) UnmarshalText(b []byte) error {
	parsed, ok := ParseID(string(b))
	if !ok {
		return NewAPIError(http.StatusBadRequest, "invalid id")
	}
	*id = parsed
	return nil
}

// isAsync は、順序付けを待たずに手元で適用してよいコマンドかどうか。
func (c *Cmd) isAsync() bool {
	switch c.Kind {
	case CmdLogin, CmdLogout, CmdBan, CmdAdminLogin, CmdAdminLogout:
		return true
	}
	return false
}

// 適用の結果（レンダリングする側へ渡す値）

type friendRequestResult struct {
	RelationID ID
	Status     string
	UpdatedAt  int64
}

// apply はコマンドを1つ適用する。ts は順序付け役が決めた時刻（マイクロ秒）。
// 拒否するときは状態を変えずにエラーを返す。呼ぶ側が Lock を持つ。
func (s *State) apply(c *Cmd, ts int64) (any, error) {
	switch c.Kind {
	case CmdRegister:
		return s.applyRegister(c, ts)
	case CmdLogin:
		return s.applyLogin(c)
	case CmdLogout:
		return s.applyLogout(c)
	case CmdUpdateMe:
		return s.applyUpdateMe(c)
	case CmdSetProfileImage:
		return s.applySetProfileImage(c)
	case CmdDeleteProfileImage:
		return s.applyDeleteProfileImage(c)
	case CmdDeleteAccount:
		return s.applyDeleteAccount(c)
	case CmdCreatePost:
		return s.applyCreatePost(c, ts)
	case CmdUpdatePost:
		return s.applyUpdatePost(c)
	case CmdDeletePost:
		return s.applyDeletePost(c)
	case CmdCreateComment:
		return s.applyCreateComment(c, ts)
	case CmdDeleteComment:
		return s.applyDeleteComment(c)
	case CmdReplaceRealmoji:
		return s.applyReplaceRealmoji(c, ts)
	case CmdDeleteRealmoji:
		return s.applyDeleteRealmoji(c)
	case CmdFriendRequest:
		return s.applyFriendRequest(c, ts)
	case CmdTransitionRequest:
		return s.applyTransition(c, ts)
	case CmdDeleteFriendship:
		return s.applyDeleteFriendship(c)
	case CmdFollow:
		return s.applyFollow(c, ts)
	case CmdUnfollow:
		return s.applyUnfollow(c)
	case CmdBlock:
		return s.applyBlock(c, ts)
	case CmdUnblock:
		return s.applyUnblock(c)
	case CmdAdminLogin:
		return s.applyAdminLogin(c)
	case CmdAdminLogout:
		return s.applyAdminLogout(c)
	case CmdCertify:
		return s.applyCertify(c)
	case CmdOpenWindow:
		return s.applyOpenWindow(c, ts)
	case CmdBan:
		return s.applyBan(c)
	}
	return nil, NewAPIError(http.StatusInternalServerError, "unknown_command")
}

func (s *State) applyRegister(c *Cmd, ts int64) (any, error) {
	if _, exists := s.byName[c.Str1]; exists {
		return nil, NewAPIError(http.StatusConflict, "username_taken")
	}
	u := &User{
		ID: c.User, IDStr: c.User.String(), Idx: -1, Username: c.Str1, SecretHash: c.Token,
		DisplayName: c.Str2, Region: c.Str3, Active: true,
		FoldUser: normalizeSearchText(c.Str1), FoldName: normalizeSearchText(c.Str2),
	}
	key, _ := hex.DecodeString(c.Token)
	copy(u.Key[:], key)
	s.users[u.ID] = u
	s.byName[u.Username] = u
	s.insertSorted(u)
	return u, nil
}

func tokenKey(hexHash string) [32]byte {
	var k [32]byte
	b, _ := hex.DecodeString(hexHash)
	copy(k[:], b)
	return k
}

func (s *State) applyLogin(c *Cmd) (any, error) {
	u := s.users[c.User]
	if u == nil {
		return nil, nil
	}
	// 同時のログインはどのノードでも時刻の大きいほうが残る（適用の順序によらない）
	if u.Session != nil && u.Session.T > c.T {
		return nil, nil
	}
	if u.Session != nil {
		delete(s.sessions, u.Session.Hash)
	}
	k := tokenKey(c.Token)
	u.Session = &UserSession{Hash: k, T: c.T}
	if u.Active {
		s.sessions[k] = u
	}
	return nil, nil
}

func (s *State) applyLogout(c *Cmd) (any, error) {
	u := s.users[c.User]
	if u == nil {
		return nil, nil
	}
	k := tokenKey(c.Token)
	if u.Session != nil && u.Session.Hash == k {
		u.Session = nil
	}
	delete(s.sessions, k)
	return nil, nil
}

func (s *State) applyUpdateMe(c *Cmd) (any, error) {
	u := s.users[c.User]
	if u == nil || !u.Active {
		return nil, NewAPIError(http.StatusNotFound, "account_not_found")
	}
	if c.P1 != nil {
		u.DisplayName = *c.P1
		u.FoldName = normalizeSearchText(*c.P1)
	}
	if c.P2 != nil {
		u.Bio = *c.P2
	}
	if c.P3 != nil && *c.P3 != u.Region {
		u.Region = *c.P3
		s.closeSSE(u)
	}
	return u, nil
}

func (s *State) applySetProfileImage(c *Cmd) (any, error) {
	u := s.users[c.User]
	if u == nil {
		return nil, NewAPIError(http.StatusNotFound, "account_not_found")
	}
	if u.HasProfile {
		delete(s.images, u.ProfileImg)
	}
	u.HasProfile = true
	u.ProfileImg = c.Img
	u.ProfileSig = decodeSig(c.ImgSig)
	u.ProfileSigExp = c.ImgExp
	s.images[c.Img] = &LiveImage{ID: c.Img, Owner: u, Kind: ImageKindProfile, Len: c.ImgLen, CRC: c.ImgCRC, Origin: c.ImgOrigin, Sig: u.ProfileSig, SigExp: c.ImgExp}
	return nil, nil
}

func (s *State) applyDeleteProfileImage(c *Cmd) (any, error) {
	u := s.users[c.User]
	if u == nil || !u.HasProfile {
		return nil, NewAPIError(http.StatusNotFound, "account_not_found")
	}
	delete(s.images, u.ProfileImg)
	u.HasProfile = false
	u.ProfileImg = ZeroID
	return nil, nil
}

func (s *State) applyDeleteAccount(c *Cmd) (any, error) {
	u := s.users[c.User]
	if u == nil || !u.Active {
		return nil, NewAPIError(http.StatusNotFound, "account_not_found")
	}
	u.Active = false
	if u.Session != nil {
		delete(s.sessions, u.Session.Hash)
		u.Session = nil
	}
	s.closeSSE(u)
	return nil, nil
}

func (s *State) closeSSE(u *User) {
	for sub := range u.SSE {
		delete(s.sseByRegion[sub.user.Region], sub)
		close(sub.gone)
	}
	u.SSE = nil
}

func removePost(list []*Post, p *Post) []*Post {
	for i, q := range list {
		if q == p {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}

func (s *State) applyCreatePost(c *Cmd, ts int64) (any, error) {
	u := s.users[c.User]
	if u == nil {
		return nil, NewAPIError(http.StatusInternalServerError, "user_not_found")
	}
	w := s.current[u.Region]
	if w == nil {
		return nil, NewAPIError(http.StatusConflict, "post_slot_unavailable")
	}
	d := u.Days[w.LocalDate]
	if d != nil && (d.Active != 0 || d.Deleted >= 2) {
		return nil, NewAPIError(http.StatusConflict, "post_slot_unavailable")
	}
	p := &Post{
		ID: c.Obj, IDStr: c.Obj.String(), Author: u, Window: w, ImageID: c.Img,
		ImageSig: decodeSig(c.ImgSig), ImageSigExp: c.ImgExp,
		Caption: c.Str1, Vis: c.Vis, PostedAt: ts,
	}
	if c.Loc != nil {
		p.Loc = PostLoc{Kind: c.Loc.Kind, HasCoords: c.Loc.HasCoords, Lat: c.Loc.Lat, Lon: c.Loc.Lon, Locality: c.Loc.Locality}
	}
	if d == nil {
		d = &DayStat{}
		if u.Days == nil {
			u.Days = make(map[int32]*DayStat, 8)
		}
		u.Days[w.LocalDate] = d
	}
	d.Active++
	u.Posts = append(u.Posts, p)
	u.ActivePosts = append(u.ActivePosts, p)
	w.Posts = append(w.Posts, p)
	s.posts[p.ID] = p
	s.images[c.Img] = &LiveImage{ID: c.Img, Owner: u, Kind: ImageKindPost, Post: p, Len: c.ImgLen, CRC: c.ImgCRC, Origin: c.ImgOrigin, Sig: p.ImageSig, SigExp: c.ImgExp}
	return p, nil
}

func (s *State) applyUpdatePost(c *Cmd) (any, error) {
	u := s.users[c.User]
	p := s.posts[c.Obj]
	if p == nil || p.Deleted {
		hp, _ := s.histPost(c.Obj)
		if hp == nil {
			return nil, NewAPIError(http.StatusNotFound, "post_not_found")
		}
		// 過去の投稿の投稿期間は終わっているので、変更はできない
		return nil, NewAPIError(http.StatusForbidden, "post_not_editable")
	}
	if p.Author != u {
		return nil, NewAPIError(http.StatusForbidden, "post_not_editable")
	}
	if p.Window.Ended {
		return nil, NewAPIError(http.StatusForbidden, "post_not_editable")
	}
	if c.P1 != nil {
		p.Caption = *c.P1
	}
	if c.P2 != nil {
		for i, name := range visibilityNames {
			if name == *c.P2 {
				p.Vis = uint8(i)
			}
		}
	}
	return p, nil
}

func (s *State) applyDeletePost(c *Cmd) (any, error) {
	u := s.users[c.User]
	p := s.posts[c.Obj]
	if p == nil || p.Deleted {
		hp, idx := s.histPost(c.Obj)
		if hp == nil {
			return nil, NewAPIError(http.StatusNotFound, "post_not_found")
		}
		if s.initUsers[hp.Author] != u {
			return nil, NewAPIError(http.StatusForbidden, "post_not_editable")
		}
		deletions := 0
		for i := s.pack.Init.Users[hp.Author].PostStart; i < s.pack.Init.Users[hp.Author].PostEnd; i++ {
			q := &s.pack.Posts[i]
			if q.LocalDate == hp.LocalDate && (q.DeletedInPack() || s.histPostDeleted[i]) {
				deletions++
			}
		}
		if deletions >= 2 {
			return nil, NewAPIError(http.StatusConflict, "post_slot_unavailable")
		}
		s.histPostDeleted[idx] = true
		return nil, nil
	}
	if p.Author != u {
		return nil, NewAPIError(http.StatusForbidden, "post_not_editable")
	}
	d := u.Days[p.Window.LocalDate]
	if d.Deleted >= 2 {
		return nil, NewAPIError(http.StatusConflict, "post_slot_unavailable")
	}
	p.Deleted = true
	d.Active--
	d.Deleted++
	u.ActivePosts = removePost(u.ActivePosts, p)
	return nil, nil
}

func (s *State) applyCreateComment(c *Cmd, ts int64) (any, error) {
	u := s.users[c.User]
	p := s.posts[c.Obj]
	if p == nil || u == nil || !s.canCommentLive(u, p) {
		return nil, NewAPIError(http.StatusForbidden, "interaction_forbidden")
	}
	cm := &Comment{ID: c.Obj2, IDStr: c.Obj2.String(), Post: p, Author: u, Body: c.Str1, CreatedAt: ts}
	p.Comments = append(p.Comments, cm)
	s.comments[cm.ID] = cm
	return cm, nil
}

func (s *State) applyDeleteComment(c *Cmd) (any, error) {
	u := s.users[c.User]
	if cm := s.comments[c.Obj]; cm != nil {
		if cm.Deleted {
			return nil, NewAPIError(http.StatusNotFound, "interaction_not_found")
		}
		if u != cm.Author && u != cm.Post.Author {
			return nil, NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		cm.Deleted = true
		return nil, nil
	}
	idx, ok := findIndex(s.pack.CommentIDs, c.Obj)
	if !ok {
		return nil, NewAPIError(http.StatusNotFound, "interaction_not_found")
	}
	hc := &s.pack.Comments[idx]
	if hc.Deleted != 0 || s.histCommentDeleted[idx] {
		return nil, NewAPIError(http.StatusNotFound, "interaction_not_found")
	}
	hp := &s.pack.Posts[hc.Post]
	if u != s.initUsers[hc.Author] && u != s.initUsers[hp.Author] {
		return nil, NewAPIError(http.StatusForbidden, "interaction_forbidden")
	}
	s.histCommentDeleted[idx] = true
	return nil, nil
}

func (s *State) applyReplaceRealmoji(c *Cmd, ts int64) (any, error) {
	u := s.users[c.User]
	p := s.posts[c.Obj]
	if p == nil || p.Deleted || u == nil {
		return nil, NewAPIError(http.StatusForbidden, "interaction_forbidden")
	}
	if u == p.Author {
		return nil, NewAPIError(http.StatusForbidden, "interaction_forbidden")
	}
	if !u.Active || !p.Author.Active {
		return nil, NewAPIError(http.StatusNotFound, "relationship_not_found")
	}
	if !s.canViewLive(u, p, s.hasPostedToday(u)) {
		return nil, NewAPIError(http.StatusForbidden, "interaction_forbidden")
	}
	for _, r := range p.Realmojis {
		if r.Reactor == u && !r.Replaced && !r.Deleted {
			r.Replaced = true
			delete(s.images, r.ImageID)
		}
	}
	rm := &Realmoji{ID: c.Obj2, IDStr: c.Obj2.String(), Post: p, Reactor: u, ImageID: c.Img, ImageSig: decodeSig(c.ImgSig), ImageSigExp: c.ImgExp, Type: c.Vis, CreatedAt: ts}
	p.Realmojis = append(p.Realmojis, rm)
	s.realmojis[rm.ID] = rm
	s.images[c.Img] = &LiveImage{ID: c.Img, Owner: u, Kind: ImageKindRealmoji, RM: rm, Len: c.ImgLen, CRC: c.ImgCRC, Origin: c.ImgOrigin, Sig: rm.ImageSig, SigExp: c.ImgExp}
	return rm, nil
}

func (s *State) applyDeleteRealmoji(c *Cmd) (any, error) {
	u := s.users[c.User]
	if rm := s.realmojis[c.Obj]; rm != nil {
		if rm.Deleted {
			return nil, NewAPIError(http.StatusNotFound, "interaction_not_found")
		}
		if u != rm.Reactor && u != rm.Post.Author {
			return nil, NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		rm.Deleted = true
		delete(s.images, rm.ImageID)
		return nil, nil
	}
	idx, ok := findIndex(s.pack.RMIDs, c.Obj)
	if !ok {
		return nil, NewAPIError(http.StatusNotFound, "interaction_not_found")
	}
	hr := &s.pack.RMs[idx]
	if s.histRMDeleted[idx] {
		return nil, NewAPIError(http.StatusNotFound, "interaction_not_found")
	}
	hp := &s.pack.Posts[hr.Post]
	if u != s.initUsers[hr.Reactor] && u != s.initUsers[hp.Author] {
		return nil, NewAPIError(http.StatusForbidden, "interaction_forbidden")
	}
	s.histRMDeleted[idx] = true
	return nil, nil
}

// 関係の変更で共通の、両者が有効であることの確認（beginRelationshipTx 相当）
func (s *State) relationPair(actorID, targetID ID) (*User, *User, error) {
	if actorID == targetID {
		return nil, nil, NewAPIError(http.StatusBadRequest, "invalid_relationship")
	}
	a, t := s.users[actorID], s.users[targetID]
	if a == nil || t == nil || !a.Active || !t.Active {
		return nil, nil, NewAPIError(http.StatusNotFound, "relationship_not_found")
	}
	return a, t, nil
}

func (s *State) addFriendship(a, b *User, id ID, ts int64) {
	if _, ok := a.Friends[b]; ok {
		return
	}
	r := &Relation{ID: id, UpdatedAt: ts, A: a, B: b}
	addRel(&a.Friends, b, r)
	addRel(&b.Friends, a, r)
}

func removeRequestLinks(r *FriendRequest) {
	delete(r.Requester.Outgoing, r.Recipient)
	delete(r.Recipient.Incoming, r.Requester)
}

func (s *State) applyFriendRequest(c *Cmd, ts int64) (any, error) {
	a, t, err := s.relationPair(c.User, c.Target)
	if err != nil {
		return nil, err
	}
	if a.isBlockedWith(t) {
		return nil, NewAPIError(http.StatusConflict, "relationship_conflict")
	}
	if _, ok := a.Friends[t]; ok {
		return nil, NewAPIError(http.StatusConflict, "relationship_conflict")
	}
	if _, ok := a.Outgoing[t]; ok {
		return nil, NewAPIError(http.StatusConflict, "relationship_conflict")
	}
	if r, ok := a.Incoming[t]; ok {
		r.Status = "accepted"
		r.UpdatedAt = ts
		removeRequestLinks(r)
		s.addFriendship(a, t, c.Obj2, ts)
		return &friendRequestResult{RelationID: r.ID, Status: r.Status, UpdatedAt: r.UpdatedAt}, nil
	}
	r := &FriendRequest{ID: c.Obj, Requester: a, Recipient: t, Status: "pending", UpdatedAt: ts}
	s.requests[r.ID] = r
	addReq(&a.Outgoing, t, r)
	addReq(&t.Incoming, a, r)
	return &friendRequestResult{RelationID: r.ID, Status: r.Status, UpdatedAt: r.UpdatedAt}, nil
}

func (s *State) applyTransition(c *Cmd, ts int64) (any, error) {
	r := s.requests[c.Obj]
	if r == nil {
		return nil, NewAPIError(http.StatusNotFound, "relationship_not_found")
	}
	actor := s.users[c.User]
	status := c.Str1
	if (status == "cancelled" && actor != r.Requester) || (status != "cancelled" && actor != r.Recipient) {
		return nil, NewAPIError(http.StatusForbidden, "relationship_action_forbidden")
	}
	if !r.Requester.Active || !r.Recipient.Active {
		return nil, NewAPIError(http.StatusNotFound, "relationship_not_found")
	}
	if r.Status != "pending" {
		return nil, NewAPIError(http.StatusForbidden, "relationship_action_forbidden")
	}
	if status == "accepted" {
		if r.Requester.isBlockedWith(r.Recipient) {
			return nil, NewAPIError(http.StatusConflict, "relationship_conflict")
		}
		s.addFriendship(r.Requester, r.Recipient, c.Obj2, ts)
	}
	r.Status = status
	r.UpdatedAt = ts
	removeRequestLinks(r)
	return &friendRequestResult{RelationID: r.ID, Status: r.Status, UpdatedAt: r.UpdatedAt}, nil
}

func (s *State) applyDeleteFriendship(c *Cmd) (any, error) {
	a, t, err := s.relationPair(c.User, c.Target)
	if err != nil {
		return nil, err
	}
	if _, ok := a.Friends[t]; !ok {
		return nil, NewAPIError(http.StatusConflict, "relationship_conflict")
	}
	delete(a.Friends, t)
	delete(t.Friends, a)
	return nil, nil
}

func (s *State) applyFollow(c *Cmd, ts int64) (any, error) {
	a, t, err := s.relationPair(c.User, c.Target)
	if err != nil {
		return nil, err
	}
	if !t.Certified || a.isBlockedWith(t) {
		return nil, NewAPIError(http.StatusConflict, "relationship_conflict")
	}
	if _, ok := a.Following[t]; ok {
		return nil, NewAPIError(http.StatusConflict, "relationship_conflict")
	}
	r := &Relation{ID: c.Obj, UpdatedAt: ts, A: a, B: t}
	addRel(&a.Following, t, r)
	addRel(&t.Followers, a, r)
	return nil, nil
}

func (s *State) applyUnfollow(c *Cmd) (any, error) {
	a, t, err := s.relationPair(c.User, c.Target)
	if err != nil {
		return nil, err
	}
	if _, ok := a.Following[t]; !ok {
		return nil, NewAPIError(http.StatusConflict, "relationship_conflict")
	}
	delete(a.Following, t)
	delete(t.Followers, a)
	return nil, nil
}

func (s *State) applyBlock(c *Cmd, ts int64) (any, error) {
	a, t, err := s.relationPair(c.User, c.Target)
	if err != nil {
		return nil, err
	}
	if _, ok := a.Blocking[t]; ok {
		return nil, NewAPIError(http.StatusConflict, "relationship_conflict")
	}
	r := &Relation{ID: c.Obj, UpdatedAt: ts, A: a, B: t}
	addRel(&a.Blocking, t, r)
	addRel(&t.BlockedBy, a, r)
	delete(a.Friends, t)
	delete(t.Friends, a)
	delete(a.Following, t)
	delete(t.Followers, a)
	delete(t.Following, a)
	delete(a.Followers, t)
	if q, ok := a.Outgoing[t]; ok {
		q.Status = "cancelled"
		q.UpdatedAt = ts
		removeRequestLinks(q)
	}
	if q, ok := a.Incoming[t]; ok {
		q.Status = "cancelled"
		q.UpdatedAt = ts
		removeRequestLinks(q)
	}
	return nil, nil
}

func (s *State) applyUnblock(c *Cmd) (any, error) {
	a, t, err := s.relationPair(c.User, c.Target)
	if err != nil {
		return nil, err
	}
	if _, ok := a.Blocking[t]; !ok {
		return nil, NewAPIError(http.StatusForbidden, "relationship_action_forbidden")
	}
	delete(a.Blocking, t)
	delete(t.BlockedBy, a)
	return nil, nil
}

func (s *State) applyAdminLogin(c *Cmd) (any, error) {
	if a := s.admins[c.Str1]; a != nil {
		s.adminSessions[tokenKey(c.Token)] = a
	}
	return nil, nil
}

func (s *State) applyAdminLogout(c *Cmd) (any, error) {
	delete(s.adminSessions, tokenKey(c.Token))
	return nil, nil
}

func (s *State) applyCertify(c *Cmd) (any, error) {
	u := s.users[c.Target]
	if u == nil || !u.Active || u.Certified {
		return nil, NewAPIError(http.StatusConflict, "certification_conflict")
	}
	u.Certified = true
	return nil, nil
}

func (s *State) applyOpenWindow(c *Cmd, ts int64) (any, error) {
	if s.maxDate[c.Str1] >= c.Date {
		return nil, NewAPIError(http.StatusConflict, "posting_window_conflict")
	}
	if old := s.current[c.Str1]; old != nil {
		old.Ended = true
		for _, p := range old.Posts {
			p.Author.ActivePosts = removePost(p.Author.ActivePosts, p)
		}
	}
	w := &Window{
		ID: c.Obj, IDStr: c.Obj.String(), Region: c.Str1, LocalDate: c.Date,
		LocalDateStr: daysToDate(c.Date).Format("2006-01-02"),
		StartsAt:     ts, OnTimeDeadline: ts + c.Secs*1_000_000,
	}
	s.windows[w.ID] = w
	s.current[w.Region] = w
	s.maxDate[w.Region] = c.Date
	if subs := s.sseByRegion[w.Region]; len(subs) > 0 && s.onWindowOpen != nil {
		list := make([]*sseSub, 0, len(subs))
		for sub := range subs {
			list = append(list, sub)
		}
		s.onWindowOpen(w, list)
	}
	return w, nil
}

func (s *State) applyBan(c *Cmd) (any, error) {
	old := s.bans.Load()
	if (*old)[c.Str1] >= c.T {
		return nil, nil
	}
	next := make(map[string]int64, len(*old)+1)
	for k, v := range *old {
		next[k] = v
	}
	next[c.Str1] = c.T
	s.bans.Store(&next)
	return nil, nil
}

func decodeSig(s string) [32]byte {
	var v [32]byte
	if s != "" {
		b, _ := hex.DecodeString(s)
		copy(v[:], b)
	}
	return v
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
