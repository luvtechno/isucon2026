package isureal

import (
	"archive/zip"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

// --- 投稿の作成 ---

var uploadPool = sync.Pool{New: func() any { b := make([]byte, 0, 1<<20); return &b }}

// readPart は multipart の1パートをプールのバッファへ読む。
func readPart(part *multipart.Part, bp *[]byte) ([]byte, error) {
	b := (*bp)[:0]
	for {
		if len(b) == cap(b) {
			b = append(b, 0)[:len(b)]
		}
		n, err := part.Read(b[len(b):cap(b)])
		b = b[:len(b)+n]
		if err == io.EOF {
			*bp = b
			return b, nil
		}
		if err != nil {
			*bp = b
			return nil, err
		}
	}
}

type postForm struct {
	image        []byte
	hasImage     bool
	caption      string
	visibility   string
	locationKind string
	latitude     *float64
	longitude    *float64
	locality     *string
	reactionType string
}

func parsePostForm(c *echo.Context, bp *[]byte) (*postForm, error) {
	mr, err := c.Request().MultipartReader()
	if err != nil {
		return &postForm{}, nil
	}
	form := &postForm{}
	var small [2048]byte
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return form, nil
		}
		if err != nil {
			return nil, err
		}
		name := part.FormName()
		if name == "image" {
			if part.FileName() == "" {
				part.Close()
				continue
			}
			data, err := readPart(part, bp)
			if err != nil {
				return nil, err
			}
			form.image, form.hasImage = data, true
			part.Close()
			continue
		}
		n, err := io.ReadFull(part, small[:])
		value := ""
		if err == nil {
			// 長い値（検査で落とされるもの）
			rest, err := io.ReadAll(io.LimitReader(part, 1<<20))
			if err != nil {
				return nil, err
			}
			value = string(small[:n]) + string(rest)
		} else if err == io.EOF || err == io.ErrUnexpectedEOF {
			value = string(small[:n])
		} else {
			return nil, err
		}
		part.Close()
		switch name {
		case "caption":
			form.caption = value
		case "visibility":
			form.visibility = value
		case "location_kind":
			form.locationKind = value
		case "latitude":
			if value != "" {
				v, err := strconv.ParseFloat(value, 64)
				if err != nil {
					return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid latitude")
				}
				form.latitude = &v
			}
		case "longitude":
			if value != "" {
				v, err := strconv.ParseFloat(value, 64)
				if err != nil {
					return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid longitude")
				}
				form.longitude = &v
			}
		case "locality":
			form.locality = &value
		case "reactionType":
			form.reactionType = value
		}
	}
}

func visibilityIndex(name string) (uint8, bool) {
	switch name {
	case "friends":
		return PostVisFriends, true
	case "friends_of_friends":
		return PostVisFoF, true
	case "public":
		return PostVisPublic, true
	}
	return 0, false
}

func (a *App) createPostHandler(c *echo.Context, s *session) error {
	bp := uploadPool.Get().(*[]byte)
	defer uploadPool.Put(bp)
	form, err := parsePostForm(c, bp)
	if err != nil {
		return err
	}
	if !form.hasImage || !validUserText(form.caption, 0, 1000) {
		return NewAPIError(http.StatusBadRequest, "invalid_post")
	}
	vis, ok := visibilityIndex(form.visibility)
	if !ok {
		return NewAPIError(http.StatusBadRequest, "invalid_post")
	}
	loc := PostLocJSON{}
	switch form.locationKind {
	case "none":
		if form.latitude != nil || form.longitude != nil || form.locality != nil {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		loc.Kind = LocNone
	case "exact":
		if form.latitude == nil || form.longitude == nil || form.locality != nil ||
			*form.latitude < -90 || *form.latitude > 90 || *form.longitude < -180 || *form.longitude > 180 {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		loc.Kind, loc.HasCoords, loc.Lat, loc.Lon = LocExact, true, *form.latitude, *form.longitude
	case "approximate":
		if form.latitude != nil || form.longitude != nil || form.locality == nil || !validUserText(*form.locality, 1, 100) {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
		loc.Kind, loc.Locality = LocApproximate, *form.locality
	default:
		return NewAPIError(http.StatusBadRequest, "invalid_post")
	}
	if ok, tooLarge := checkJPEG(form.image, 4096); !ok || tooLarge {
		return NewAPIError(http.StatusBadRequest, "invalid_post")
	}
	imageID := newID()
	if err := a.imgs.Write(imageID, form.image); err != nil {
		return err
	}
	exp := currentImageExp(time.Now())
	sig := FullImageSignature(&s.user.Key, imageID, exp)
	res, err := a.repl.Submit(&Cmd{
		Kind: CmdCreatePost, User: s.user.ID, Obj: newID(), Img: imageID, ImgLen: uint32(len(form.image)),
		ImgCRC: crc32.ChecksumIEEE(form.image), ImgOrigin: a.cfg.Node, ImgSig: hex.EncodeToString(sig[:]), ImgExp: exp,
		Str1: form.caption, Vis: vis, Loc: &loc,
	})
	if err != nil {
		a.imgs.Remove(imageID)
		return err
	}
	p := res.(*Post)
	r := newRenderer(a.st, make([]byte, 0, 1024))
	a.st.mu.RLock()
	r.post(p)
	posted := len(p.Window.Posts)
	a.st.mu.RUnlock()
	r.raw(`}`)
	a.throttle.AfterPost(posted)
	return writeJSON(c, http.StatusCreated, r.b)
}

func (a *App) getMyCurrentPostHandler(c *echo.Context, s *session) error {
	st := a.st
	u := s.user
	r := newRenderer(a.st, make([]byte, 0, 1024))
	st.mu.RLock()
	found := false
	w := st.current[u.Region]
	if w != nil {
		for i := len(u.Posts) - 1; i >= 0; i-- {
			p := u.Posts[i]
			if !p.Deleted && p.Window.LocalDate == w.LocalDate {
				r.post(p)
				r.raw(`}`)
				found = true
				break
			}
		}
	}
	st.mu.RUnlock()
	if !found {
		return NewAPIError(http.StatusNotFound, "post_not_found")
	}
	return writeJSON(c, http.StatusOK, r.b)
}

func (a *App) getPostHandler(c *echo.Context, s *session) error {
	st := a.st
	id, ok := parseUUIDParam(c.Param("postId"))
	if !ok {
		return NewAPIError(http.StatusNotFound, "post_not_found")
	}
	r := newRenderer(a.st, make([]byte, 0, 1024))
	st.mu.RLock()
	found := false
	if p := st.posts[id]; p != nil {
		if !p.Deleted && p.Author.Active && st.canViewLive(s.user, p, st.hasPostedToday(s.user)) {
			r.post(p)
			r.raw(`}`)
			found = true
		}
	} else if hp, idx := st.histPost(id); hp != nil && st.initUsers[hp.Author] == s.user {
		r.histPost(st, hp, idx)
		r.raw(`}`)
		found = true
	}
	st.mu.RUnlock()
	if !found {
		return NewAPIError(http.StatusNotFound, "post_not_found")
	}
	return writeJSON(c, http.StatusOK, r.b)
}

func (a *App) updatePostHandler(c *echo.Context, s *session) error {
	var input struct {
		Caption    *string `json:"caption"`
		Visibility *string `json:"visibility"`
	}
	if err := c.Bind(&input); err != nil {
		return err
	}
	if input.Caption == nil && input.Visibility == nil {
		return NewAPIError(http.StatusBadRequest, "invalid_post")
	}
	if input.Caption != nil && !validUserText(*input.Caption, 0, 1000) {
		return NewAPIError(http.StatusBadRequest, "invalid_post")
	}
	if input.Visibility != nil {
		if _, ok := visibilityIndex(*input.Visibility); !ok {
			return NewAPIError(http.StatusBadRequest, "invalid_post")
		}
	}
	id, ok := parseUUIDParam(c.Param("postId"))
	if !ok {
		return NewAPIError(http.StatusNotFound, "post_not_found")
	}
	res, err := a.repl.Submit(&Cmd{Kind: CmdUpdatePost, User: s.user.ID, Obj: id, P1: input.Caption, P2: input.Visibility})
	if err != nil {
		return err
	}
	p := res.(*Post)
	r := newRenderer(a.st, make([]byte, 0, 1024))
	a.st.mu.RLock()
	r.post(p)
	a.st.mu.RUnlock()
	r.raw(`}`)
	return writeJSON(c, http.StatusOK, r.b)
}

func (a *App) deletePostHandler(c *echo.Context, s *session) error {
	id, ok := parseUUIDParam(c.Param("postId"))
	if !ok {
		return NewAPIError(http.StatusNotFound, "post_not_found")
	}
	if _, err := a.repl.Submit(&Cmd{Kind: CmdDeletePost, User: s.user.ID, Obj: id}); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (a *App) getCurrentPostingWindowHandler(c *echo.Context, s *session) error {
	st := a.st
	u := s.user
	b := make([]byte, 0, 256)
	st.mu.RLock()
	w := st.current[u.Region]
	if w == nil {
		st.mu.RUnlock()
		b = append(b, `{"window":null,"hasPosted":false,"canPost":false,"deletionCount":0}`...)
		return writeJSON(c, http.StatusOK, b)
	}
	var active, deleted int32
	if d := u.Days[w.LocalDate]; d != nil {
		active, deleted = d.Active, d.Deleted
	}
	b = append(b, `{"window":{"id":"`...)
	b = append(b, w.IDStr...)
	b = append(b, `","region":"`...)
	b = append(b, w.Region...)
	b = append(b, `","localDate":"`...)
	b = append(b, w.LocalDateStr...)
	b = append(b, `","startsAt":`...)
	b = appendTimeUS(b, w.StartsAt)
	b = append(b, `,"onTimeDeadline":`...)
	b = appendTimeUS(b, w.OnTimeDeadline)
	b = append(b, `},"hasPosted":`...)
	b = append(b, boolStr(active > 0)...)
	b = append(b, `,"canPost":`...)
	b = append(b, boolStr(active == 0 && deleted < 2 && time.Now().UnixMicro() >= w.StartsAt)...)
	b = append(b, `,"deletionCount":`...)
	b = strconv.AppendInt(b, int64(deleted), 10)
	b = append(b, '}')
	st.mu.RUnlock()
	return writeJSON(c, http.StatusOK, b)
}

// --- フィード ---

type feedCursor struct {
	score *int64
	at    *int64
	id    string
}

func parseFeedQuery(q string) (*feedCursor, error) {
	cur := &feedCursor{}
	if q == "" {
		return cur, nil
	}
	values, err := url.ParseQuery(q)
	if err != nil {
		return nil, NewAPIError(http.StatusBadRequest, "invalid_pagination")
	}
	if v := values.Get("beforeScore"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		cur.score = &n
	}
	if v := values.Get("beforePostedAt"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return nil, NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		us := t.UnixMicro()
		cur.at = &us
	}
	cur.id = values.Get("beforeId")
	return cur, nil
}

func (c *feedCursor) allows(p *Post) bool {
	if c.at == nil {
		return true
	}
	return p.PostedAt < *c.at || (p.PostedAt == *c.at && p.IDStr < c.id)
}

type scoredPost struct {
	post  *Post
	score int64
}

func (a *App) getFeedHandler(c *echo.Context, s *session) error {
	feed := c.Param("feed")
	switch feed {
	case "friends", "friends-of-friends", "public", "following":
	default:
		return NewAPIError(http.StatusBadRequest, "invalid_post")
	}
	cur, err := parseFeedQuery(c.Request().URL.RawQuery)
	if err != nil {
		return err
	}
	if (cur.at == nil) != (cur.id == "") {
		return NewAPIError(http.StatusBadRequest, "invalid_pagination")
	}
	ranked := feed == "friends-of-friends"
	if ranked && (cur.score == nil) != (cur.id == "") {
		return NewAPIError(http.StatusBadRequest, "invalid_pagination")
	}
	st := a.st
	viewer := s.user
	bp := getBuf()
	defer putBuf(bp)
	r := newRenderer(a.st, (*bp)[:0])
	r.raw(`{"items":[`)
	count := 0
	hasMore := false
	emit := func(p *Post, score *int64) bool {
		if count == 50 {
			hasMore = true
			return false
		}
		if count > 0 {
			r.raw(`,`)
		}
		r.post(p)
		if score != nil {
			r.raw(`,"score":`)
			r.b = strconv.AppendInt(r.b, *score, 10)
		}
		r.raw(`}`)
		count++
		return true
	}
	st.mu.RLock()
	if st.hasPostedToday(viewer) {
		switch feed {
		case "public":
			a.feedPublic(viewer, cur, emit)
		case "friends":
			posts := make([]*Post, 0, 32)
			for f := range viewer.Friends {
				if f.Active && !viewer.isBlockedWith(f) {
					for _, p := range f.ActivePosts {
						if cur.allows(p) {
							posts = append(posts, p)
						}
					}
				}
			}
			emitSorted(posts, emit)
		case "following":
			posts := make([]*Post, 0, 16)
			for f := range viewer.Following {
				if f.Active {
					for _, p := range f.ActivePosts {
						if cur.allows(p) && st.canViewLive(viewer, p, true) {
							posts = append(posts, p)
						}
					}
				}
			}
			emitSorted(posts, emit)
		case "friends-of-friends":
			a.feedFoF(viewer, cur, emit)
		}
	}
	st.mu.RUnlock()
	r.raw(`],"hasMore":`)
	r.raw(boolStr(hasMore))
	r.raw(`}`)
	*bp = r.b
	return writeJSON(c, http.StatusOK, r.b)
}

func emitSorted(posts []*Post, emit func(*Post, *int64) bool) {
	sort.Slice(posts, func(i, j int) bool {
		if posts[i].PostedAt != posts[j].PostedAt {
			return posts[i].PostedAt > posts[j].PostedAt
		}
		return posts[i].IDStr > posts[j].IDStr
	})
	for _, p := range posts {
		if !emit(p, nil) {
			return
		}
	}
}

// feedPublic は3つのリージョンの投稿期間を新しい順に併合する。
func (a *App) feedPublic(viewer *User, cur *feedCursor, emit func(*Post, *int64) bool) {
	st := a.st
	var lists [3][]*Post
	n := 0
	for _, w := range st.current {
		lists[n] = w.Posts
		n++
	}
	idx := [3]int{len(lists[0]) - 1, len(lists[1]) - 1, len(lists[2]) - 1}
	for {
		best := -1
		for i := 0; i < n; i++ {
			if idx[i] >= 0 && (best < 0 || lists[i][idx[i]].PostedAt > lists[best][idx[best]].PostedAt) {
				best = i
			}
		}
		if best < 0 {
			return
		}
		p := lists[best][idx[best]]
		idx[best]--
		if p.Deleted || p.Vis != PostVisPublic || p.Author == viewer || !p.Author.Active || !cur.allows(p) || viewer.isBlockedWith(p.Author) {
			continue
		}
		if !emit(p, nil) {
			return
		}
	}
}

// feedFoF は友達の友達のフィード（おすすめの順）を作る。
func (a *App) feedFoF(viewer *User, cur *feedCursor, emit func(*Post, *int64) bool) {
	mutual := make(map[*User]int64, 256)
	for f := range viewer.Friends {
		for x := range f.Friends {
			if x != viewer {
				mutual[x]++
			}
		}
	}
	candidates := make([]scoredPost, 0, 32)
	for author := range mutual {
		if _, friend := viewer.Friends[author]; friend || !author.Active || len(author.ActivePosts) == 0 || viewer.isBlockedWith(author) {
			continue
		}
		var conn int64 = -1
		for _, p := range author.ActivePosts {
			if p.Vis == PostVisFriends {
				continue
			}
			if conn < 0 {
				conn = 0
				for x := range author.Friends {
					conn += mutual[x]
				}
			}
			var engaged int64
			seen := map[*User]bool{}
			for _, rm := range p.Realmojis {
				if !rm.Replaced && !rm.Deleted && rm.Reactor.Active && !seen[rm.Reactor] {
					seen[rm.Reactor] = true
					if _, ok := viewer.Friends[rm.Reactor]; ok {
						engaged++
					}
				}
			}
			for _, cm := range p.Comments {
				if !cm.Deleted && cm.Author.Active && !seen[cm.Author] {
					seen[cm.Author] = true
					if _, ok := viewer.Friends[cm.Author]; ok {
						engaged++
					}
				}
			}
			candidates = append(candidates, scoredPost{p, 10*engaged + 3*mutual[author] + min(conn, 30)})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		x, y := &candidates[i], &candidates[j]
		if x.score != y.score {
			return x.score > y.score
		}
		if x.post.PostedAt != y.post.PostedAt {
			return x.post.PostedAt > y.post.PostedAt
		}
		return x.post.IDStr > y.post.IDStr
	})
	for i := range candidates {
		x := &candidates[i]
		if cur.score != nil {
			// rankedBefore
			before := false
			if x.score != *cur.score {
				before = x.score < *cur.score
			} else if cur.at != nil && x.post.PostedAt != *cur.at {
				before = x.post.PostedAt < *cur.at
			} else {
				before = x.post.IDStr < cur.id
			}
			if !before {
				continue
			}
		}
		score := x.score
		if !emit(x.post, &score) {
			return
		}
	}
}

// --- コメント ---

func (a *App) createCommentHandler(c *echo.Context, s *session) error {
	var input struct {
		Body string `json:"body"`
	}
	if err := c.Bind(&input); err != nil {
		return err
	}
	if !validUserText(input.Body, 1, 1000) {
		return NewAPIError(http.StatusBadRequest, "invalid_interaction")
	}
	id, ok := parseUUIDParam(c.Param("postId"))
	if !ok {
		return NewAPIError(http.StatusForbidden, "interaction_forbidden")
	}
	res, err := a.repl.Submit(&Cmd{Kind: CmdCreateComment, User: s.user.ID, Obj: id, Obj2: newID(), Str1: input.Body})
	if err != nil {
		return err
	}
	cm := res.(*Comment)
	r := newRenderer(a.st, make([]byte, 0, 1024))
	a.st.mu.RLock()
	r.comment(cm.ID, cm.Author, cm.Body, cm.CreatedAt)
	a.st.mu.RUnlock()
	return writeJSON(c, http.StatusCreated, r.b)
}

func (a *App) getCommentsHandler(c *echo.Context, s *session) error {
	var input struct {
		AfterCreatedAt *time.Time `query:"afterCreatedAt"`
		AfterID        string     `query:"afterId"`
	}
	if c.Request().URL.RawQuery != "" {
		if err := c.Bind(&input); err != nil {
			return err
		}
	}
	if (input.AfterCreatedAt == nil) != (input.AfterID == "") {
		return NewAPIError(http.StatusBadRequest, "invalid_pagination")
	}
	id, ok := parseUUIDParam(c.Param("postId"))
	if !ok {
		return NewAPIError(http.StatusForbidden, "interaction_forbidden")
	}
	st := a.st
	st.mu.RLock()
	unlocked := false
	unlock := func() {
		if !unlocked {
			unlocked = true
			st.mu.RUnlock()
		}
	}
	defer unlock()
	p := st.posts[id]
	if p == nil || !st.canCommentLive(s.user, p) {
		return NewAPIError(http.StatusForbidden, "interaction_forbidden")
	}
	var after int64
	if input.AfterCreatedAt != nil {
		after = input.AfterCreatedAt.UnixMicro()
	}
	bp := getBuf()
	defer putBuf(bp)
	r := newRenderer(a.st, (*bp)[:0])
	r.raw(`{"items":[`)
	count := 0
	hasMore := false
	for _, cm := range p.Comments {
		if cm.Deleted || !cm.Author.Active {
			continue
		}
		if input.AfterCreatedAt != nil && !(cm.CreatedAt > after || (cm.CreatedAt == after && cm.IDStr > input.AfterID)) {
			continue
		}
		if count == 50 {
			hasMore = true
			break
		}
		if count > 0 {
			r.raw(`,`)
		}
		r.comment(cm.ID, cm.Author, cm.Body, cm.CreatedAt)
		count++
	}
	unlock()
	r.raw(`],"hasMore":`)
	r.raw(boolStr(hasMore))
	r.raw(`}`)
	*bp = r.b
	return writeJSON(c, http.StatusOK, r.b)
}

func (a *App) deleteCommentHandler(c *echo.Context, s *session) error {
	id, ok := parseUUIDParam(c.Param("commentId"))
	if !ok {
		return NewAPIError(http.StatusNotFound, "interaction_not_found")
	}
	if _, err := a.repl.Submit(&Cmd{Kind: CmdDeleteComment, User: s.user.ID, Obj: id}); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// --- RealMoji ---

func (a *App) replaceRealMojiHandler(c *echo.Context, s *session) error {
	bp := uploadPool.Get().(*[]byte)
	defer uploadPool.Put(bp)
	form, err := parsePostForm(c, bp)
	if err != nil {
		return err
	}
	if !form.hasImage {
		return NewAPIError(http.StatusBadRequest, "invalid_realmoji")
	}
	switch form.reactionType {
	case "like", "smile", "surprise", "heart", "laugh", "instant":
	default:
		return NewAPIError(http.StatusBadRequest, "invalid_realmoji")
	}
	if ok, tooLarge := checkJPEG(form.image, 512); !ok || tooLarge {
		return NewAPIError(http.StatusBadRequest, "invalid_realmoji")
	}
	id, ok := parseUUIDParam(c.Param("postId"))
	if !ok {
		return NewAPIError(http.StatusForbidden, "interaction_forbidden")
	}
	imageID := newID()
	if err := a.imgs.Write(imageID, form.image); err != nil {
		return err
	}
	exp := currentImageExp(time.Now())
	sig := FullImageSignature(&s.user.Key, imageID, exp)
	res, err := a.repl.Submit(&Cmd{
		Kind: CmdReplaceRealmoji, User: s.user.ID, Obj: id, Obj2: newID(), Img: imageID, ImgLen: uint32(len(form.image)),
		ImgCRC: crc32.ChecksumIEEE(form.image), ImgOrigin: a.cfg.Node, ImgSig: hex.EncodeToString(sig[:]), ImgExp: exp, Vis: ReactionTypeIndex(form.reactionType),
	})
	if err != nil {
		a.imgs.Remove(imageID)
		return err
	}
	rm := res.(*Realmoji)
	r := newRenderer(a.st, make([]byte, 0, 1024))
	a.st.mu.RLock()
	r.realmoji(rm.ID, rm.Reactor, rm.Type, rm.ImageID, rm.CreatedAt, &rm.ImageSig, rm.ImageSigExp)
	a.st.mu.RUnlock()
	return writeJSON(c, http.StatusCreated, r.b)
}

func (a *App) getRealMojisHandler(c *echo.Context, s *session) error {
	var input struct {
		BeforeCreatedAt *time.Time `query:"beforeCreatedAt"`
		BeforeID        string     `query:"beforeId"`
	}
	if c.Request().URL.RawQuery != "" {
		if err := c.Bind(&input); err != nil {
			return err
		}
	}
	if (input.BeforeCreatedAt == nil) != (input.BeforeID == "") {
		return NewAPIError(http.StatusBadRequest, "invalid_pagination")
	}
	id, ok := parseUUIDParam(c.Param("postId"))
	if !ok {
		return NewAPIError(http.StatusForbidden, "interaction_forbidden")
	}
	st := a.st
	st.mu.RLock()
	unlocked := false
	unlock := func() {
		if !unlocked {
			unlocked = true
			st.mu.RUnlock()
		}
	}
	defer unlock()
	var before int64
	if input.BeforeCreatedAt != nil {
		before = input.BeforeCreatedAt.UnixMicro()
	}
	bp := getBuf()
	defer putBuf(bp)
	r := newRenderer(a.st, (*bp)[:0])
	r.raw(`{"items":[`)
	count := 0
	hasMore := false
	if p := st.posts[id]; p != nil {
		if p.Deleted || !p.Author.Active || !st.canViewLive(s.user, p, st.hasPostedToday(s.user)) {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		for i := len(p.Realmojis) - 1; i >= 0; i-- {
			rm := p.Realmojis[i]
			if rm.Replaced || rm.Deleted || !rm.Reactor.Active {
				continue
			}
			if input.BeforeCreatedAt != nil && !(rm.CreatedAt < before || (rm.CreatedAt == before && rm.IDStr < input.BeforeID)) {
				continue
			}
			if count == 50 {
				hasMore = true
				break
			}
			if count > 0 {
				r.raw(`,`)
			}
			r.realmoji(rm.ID, rm.Reactor, rm.Type, rm.ImageID, rm.CreatedAt, &rm.ImageSig, rm.ImageSigExp)
			count++
		}
	} else {
		hp, _ := st.histPost(id)
		if hp == nil || st.initUsers[hp.Author] != s.user {
			return NewAPIError(http.StatusForbidden, "interaction_forbidden")
		}
		for i := hp.RMStart; i < hp.RMEnd; i++ {
			rm := &st.pack.RMs[i]
			rmIdx := i
			reactor := st.initUsers[rm.Reactor]
			if rm.Replaced != 0 || st.histRMDeleted[i] || !reactor.Active {
				continue
			}
			if input.BeforeCreatedAt != nil && !(rm.CreatedAt < before || (rm.CreatedAt == before && rm.ID.String() < input.BeforeID)) {
				continue
			}
			if count == 50 {
				hasMore = true
				break
			}
			if count > 0 {
				r.raw(`,`)
			}
			r.realmoji(rm.ID, reactor, rm.Type, rm.ImageID, rm.CreatedAt, &st.pack.RMSigs[rmIdx], st.pack.SigExp)
			count++
		}
	}
	unlock()
	r.raw(`],"hasMore":`)
	r.raw(boolStr(hasMore))
	r.raw(`}`)
	*bp = r.b
	return writeJSON(c, http.StatusOK, r.b)
}

func (a *App) deleteRealMojiHandler(c *echo.Context, s *session) error {
	id, ok := parseUUIDParam(c.Param("realMojiId"))
	if !ok {
		return NewAPIError(http.StatusNotFound, "interaction_not_found")
	}
	if _, err := a.repl.Submit(&Cmd{Kind: CmdDeleteRealmoji, User: s.user.ID, Obj: id}); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// --- 通知 ---

func (a *App) getNotificationsHandler(c *echo.Context, s *session) error {
	ctx := c.Request().Context()
	sub := &sseSub{user: s.user, ch: make(chan *Window, 1), gone: make(chan struct{})}
	st := a.st
	st.mu.Lock()
	region := s.user.Region
	if st.sseByRegion[region] == nil {
		st.sseByRegion[region] = map[*sseSub]struct{}{}
	}
	st.sseByRegion[region][sub] = struct{}{}
	if s.user.SSE == nil {
		s.user.SSE = map[*sseSub]struct{}{}
	}
	s.user.SSE[sub] = struct{}{}
	st.mu.Unlock()
	defer func() {
		st.mu.Lock()
		delete(st.sseByRegion[region], sub)
		delete(s.user.SSE, sub)
		st.mu.Unlock()
	}()
	response := c.Response()
	response.Header().Set("Content-Type", "text/event-stream")
	response.WriteHeader(http.StatusOK)
	controller := http.NewResponseController(response)
	if err := controller.Flush(); err != nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return nil
	case <-sub.gone:
		return nil
	case w := <-sub.ch:
		b := make([]byte, 0, 256)
		b = append(b, `data: {"id":"`...)
		b = append(b, uuid.NewString()...)
		b = append(b, `","postingWindowId":"`...)
		b = append(b, w.IDStr...)
		b = append(b, `","region":"`...)
		b = append(b, w.Region...)
		b = append(b, `","createdAt":`...)
		b = appendTimeUS(b, w.StartsAt)
		b = append(b, "}\n\n"...)
		if _, err := response.Write(b); err != nil {
			return nil
		}
		_ = controller.Flush()
		return nil
	}
}

// --- Memories ---

func (a *App) getMemoriesHandler(c *echo.Context, s *session) error {
	month := c.QueryParam("month")
	start, err := time.Parse("2006-01", month)
	if err != nil {
		return NewAPIError(http.StatusBadRequest, "invalid_memory_period")
	}
	return a.listMemories(c, s, dateToDays(start), dateToDays(start.AddDate(0, 1, 0)))
}

func (a *App) getMemoriesByDateHandler(c *echo.Context, s *session) error {
	start, err := time.Parse(time.DateOnly, c.Param("date"))
	if err != nil {
		return NewAPIError(http.StatusBadRequest, "invalid_memory_period")
	}
	return a.listMemories(c, s, dateToDays(start), dateToDays(start.AddDate(0, 0, 1)))
}

func (a *App) listMemories(c *echo.Context, s *session, start, end int32) error {
	q := c.Request().URL.Query()
	beforeID := q.Get("beforeId")
	var beforeAt int64
	hasBefore := false
	if v := q.Get("beforePostedAt"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return NewAPIError(http.StatusBadRequest, "invalid_pagination")
		}
		beforeAt, hasBefore = t.UnixMicro(), true
	}
	if hasBefore != (beforeID != "") {
		return NewAPIError(http.StatusBadRequest, "invalid_pagination")
	}
	st := a.st
	u := s.user
	bp := getBuf()
	defer putBuf(bp)
	r := newRenderer(a.st, (*bp)[:0])
	r.raw(`{"items":[`)
	count := 0
	hasMore := false
	st.mu.RLock()
	// 新しい投稿から順に。この期間の投稿は初期データより必ず新しい。
	for i := len(u.Posts) - 1; i >= 0 && !hasMore; i-- {
		p := u.Posts[i]
		d := p.Window.LocalDate
		if p.Deleted || d < start || d >= end {
			continue
		}
		if hasBefore && !(p.PostedAt < beforeAt || (p.PostedAt == beforeAt && p.IDStr < beforeID)) {
			continue
		}
		if count == 50 {
			hasMore = true
			break
		}
		if count > 0 {
			r.raw(`,`)
		}
		r.post(p)
		r.raw(`,"realmojis":[`)
		n := 0
		for j := len(p.Realmojis) - 1; j >= 0 && n < 50; j-- {
			rm := p.Realmojis[j]
			if rm.Replaced || rm.Deleted || !rm.Reactor.Active {
				continue
			}
			if n > 0 {
				r.raw(`,`)
			}
			r.realmoji(rm.ID, rm.Reactor, rm.Type, rm.ImageID, rm.CreatedAt, &rm.ImageSig, rm.ImageSigExp)
			n++
		}
		r.raw(`],"comments":[`)
		n = 0
		for _, cm := range p.Comments {
			if n >= 50 {
				break
			}
			if cm.Deleted || !cm.Author.Active {
				continue
			}
			if n > 0 {
				r.raw(`,`)
			}
			r.comment(cm.ID, cm.Author, cm.Body, cm.CreatedAt)
			n++
		}
		r.raw(`]}`)
		count++
	}
	if u.Idx >= 0 && !hasMore {
		iu := &st.pack.Init.Users[u.Idx]
		for i := iu.PostStart; i < iu.PostEnd; i++ {
			p := &st.pack.Posts[i]
			if p.LocalDate < start || p.LocalDate >= end || p.DeletedInPack() || st.histPostDeleted[i] {
				continue
			}
			if hasBefore && !(p.PostedAt < beforeAt || (p.PostedAt == beforeAt && p.ID.String() < beforeID)) {
				continue
			}
			if count == 50 {
				hasMore = true
				break
			}
			if count > 0 {
				r.raw(`,`)
			}
			r.histPost(st, p, i)
			r.raw(`,"realmojis":[`)
			n := 0
			for j := p.RMStart; j < p.RMEnd && n < 50; j++ {
				rm := &st.pack.RMs[j]
				rmIdx := j
				if rm.Replaced != 0 || st.histRMDeleted[j] {
					continue
				}
				reactor := st.initUsers[rm.Reactor]
				if !reactor.Active {
					continue
				}
				if n > 0 {
					r.raw(`,`)
				}
				r.realmoji(rm.ID, reactor, rm.Type, rm.ImageID, rm.CreatedAt, &st.pack.RMSigs[rmIdx], st.pack.SigExp)
				n++
			}
			r.raw(`],"comments":[`)
			n = 0
			for j := p.CStart; j < p.CEnd && n < 50; j++ {
				cm := &st.pack.Comments[j]
				if cm.Deleted != 0 || st.histCommentDeleted[j] {
					continue
				}
				author := st.initUsers[cm.Author]
				if !author.Active {
					continue
				}
				if n > 0 {
					r.raw(`,`)
				}
				r.comment(cm.ID, author, st.pack.str(cm.BodyOff, cm.BodyLen), cm.CreatedAt)
				n++
			}
			r.raw(`]}`)
			count++
		}
	}
	st.mu.RUnlock()
	r.raw(`],"hasMore":`)
	r.raw(boolStr(hasMore))
	r.raw(`}`)
	*bp = r.b
	return writeJSON(c, http.StatusOK, r.b)
}

// --- エクスポート ---

const exportBulkMax = 32 << 20

var exportBulkPool = sync.Pool{New: func() any { b := make([]byte, 0, 8<<20); return &b }}

type exportEntry struct {
	hist   *HistImage
	id     ID
	origin uint8
	length uint32
	crc    uint32
}

func (a *App) exportPostsHandler(c *echo.Context, s *session) error {
	st := a.st
	u := s.user
	entries := make([]exportEntry, 0, 64)
	r := newRenderer(a.st, make([]byte, 0, 64<<10))
	r.raw(`[`)
	st.mu.RLock()
	count := 0
	addManifest := func() {
		r.raw(`,"imagePath":"images/`)
		r.b = append(r.b, fmt.Sprintf("%06d", count+1)...)
		r.raw(`.jpg"}`)
		count++
	}
	if u.Idx >= 0 {
		iu := &st.pack.Init.Users[u.Idx]
		// 過去の投稿は新しい順に並んでいるので、後ろから読むと古い順になる
		for i := int64(iu.PostEnd) - 1; i >= int64(iu.PostStart); i-- {
			p := &st.pack.Posts[i]
			if p.DeletedInPack() || st.histPostDeleted[uint32(i)] {
				continue
			}
			e := a.pack.FindImage(p.ImageID)
			if e == nil {
				continue
			}
			if count > 0 {
				r.raw(`,`)
			}
			r.histPost(st, p, uint32(i))
			addManifest()
			entries = append(entries, exportEntry{hist: e, id: e.ID, length: e.Len, crc: e.CRC})
		}
	}
	for _, p := range u.Posts {
		if p.Deleted {
			continue
		}
		img := st.images[p.ImageID]
		if img == nil {
			continue
		}
		if count > 0 {
			r.raw(`,`)
		}
		r.post(p)
		addManifest()
		entries = append(entries, exportEntry{id: p.ImageID, origin: img.Origin, length: img.Len, crc: img.CRC})
	}
	st.mu.RUnlock()
	r.raw(`]`)
	r.raw("\n")

	total := 0
	for i := range entries {
		total += int(entries[i].length) + 128
	}
	var body bytes.Buffer
	body.Grow(total + len(r.b) + 1024)
	writer := zip.NewWriter(&body)
	// 過去の画像は持ち主ごとに固めてファイルに置いてあるので、まとめて1回で読む
	var lo, hi uint64
	lo = ^uint64(0)
	for i := range entries {
		if e := entries[i].hist; e != nil && e.Len > 0 {
			lo = min(lo, e.Off)
			hi = max(hi, e.Off+uint64(e.Len))
		}
	}
	var bulk []byte
	var bulkBuf *[]byte
	if hi > lo && hi-lo <= exportBulkMax && hi <= a.pack.imageFileSz {
		bulkBuf = exportBulkPool.Get().(*[]byte)
		defer exportBulkPool.Put(bulkBuf)
		if uint64(cap(*bulkBuf)) < hi-lo {
			*bulkBuf = make([]byte, hi-lo)
		}
		bulk = (*bulkBuf)[:hi-lo]
		if _, err := a.pack.imageFile.ReadAt(bulk, int64(lo)); err != nil {
			bulk = nil
		}
	}
	var imgBuf []byte
	for i := range entries {
		e := &entries[i]
		name := "images/" + fmt.Sprintf("%06d", i+1) + ".jpg"
		w, err := writer.CreateRaw(&zip.FileHeader{
			Name: name, Method: zip.Store, CRC32: e.crc,
			CompressedSize64: uint64(e.length), UncompressedSize64: uint64(e.length),
		})
		if err != nil {
			return err
		}
		if e.hist != nil {
			if bulk != nil {
				if _, err := w.Write(bulk[e.hist.Off-lo : e.hist.Off-lo+uint64(e.hist.Len)]); err != nil {
					return err
				}
				continue
			}
			imgBuf, err = a.imgs.ReadHist(e.hist, imgBuf[:0])
			if err != nil {
				return err
			}
			if _, err := w.Write(imgBuf); err != nil {
				return err
			}
			continue
		}
		f, err := a.imgs.Open(e.id, e.origin)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, f)
		f.Close()
		if err != nil {
			return err
		}
	}
	manifest, err := writer.CreateRaw(&zip.FileHeader{
		Name: "posts.json", Method: zip.Store, CRC32: crc32.ChecksumIEEE(r.b),
		CompressedSize64: uint64(len(r.b)), UncompressedSize64: uint64(len(r.b)),
	})
	if err != nil {
		return err
	}
	if _, err := manifest.Write(r.b); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	h := c.Response().Header()
	h.Set(echo.HeaderContentDisposition, `attachment; filename="isureal-posts.zip"`)
	h.Set(echo.HeaderCacheControl, "no-store")
	h.Set("Content-Length", strconv.Itoa(body.Len()))
	return c.Blob(http.StatusOK, "application/zip", body.Bytes())
}

var _ = json.Marshal
var _ = os.Open
