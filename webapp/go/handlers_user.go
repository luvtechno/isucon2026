package isureal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"hash/crc32"
	"image/jpeg"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

func parseUUIDParam(s string) (ID, bool) { return ParseID(s) }

func newID() ID { return ID(uuid.New()) }

type registerRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Region      string `json:"region"`
}

type loginRequest struct {
	Username  string `json:"username"`
	SecretKey string `json:"secretKey"`
}

type updateMeRequest struct {
	DisplayName *string `json:"displayName"`
	Bio         *string `json:"bio"`
	Region      *string `json:"region"`
}

func (a *App) registerHandler(c *echo.Context) error {
	var input registerRequest
	if err := c.Bind(&input); err != nil {
		return err
	}
	if !usernamePattern.MatchString(input.Username) || !validUserText(input.DisplayName, 1, 100) || !validRegion(input.Region) {
		return NewAPIError(http.StatusBadRequest, "invalid_registration")
	}
	secretKey := uuid.NewString()
	res, err := a.repl.Submit(&Cmd{
		Kind: CmdRegister, User: newID(), Str1: input.Username, Str2: input.DisplayName, Str3: input.Region,
		Token: hashToken(secretKey),
	})
	if err != nil {
		return err
	}
	u := res.(*User)
	r := newRenderer(a.st, make([]byte, 0, 512))
	r.raw(`{"secretKey":`)
	r.str(secretKey)
	r.raw(`,"user":`)
	a.st.mu.RLock()
	r.sessionUser(u)
	a.st.mu.RUnlock()
	r.raw(`}}`)
	return writeJSON(c, http.StatusCreated, r.b)
}

func (a *App) loginHandler(c *echo.Context) error {
	var input loginRequest
	if err := c.Bind(&input); err != nil {
		return err
	}
	ip := c.RealIP()
	if !usernamePattern.MatchString(input.Username) {
		a.guard.Observe(ip, failLogin)
		return NewAPIError(http.StatusUnauthorized, "invalid_credentials")
	}
	st := a.st
	a.repl.WaitFresh()
	st.mu.RLock()
	u := st.byName[input.Username]
	var hash string
	var active bool
	if u != nil {
		hash, active = u.SecretHash, u.Active
	}
	st.mu.RUnlock()
	secretHash := sha256.Sum256([]byte(input.SecretKey))
	if u == nil || !active || hash != hex.EncodeToString(secretHash[:]) {
		a.guard.Observe(ip, failLogin)
		return NewAPIError(http.StatusUnauthorized, "invalid_credentials")
	}
	token := uuid.NewString()
	a.repl.SubmitAsync(&Cmd{Kind: CmdLogin, User: u.ID, Token: hashToken(token), T: time.Now().UnixMicro()}, 0)
	bp := getBuf()
	defer putBuf(bp)
	r := &renderer{b: (*bp)[:0]}
	r.raw(`{"token":"`)
	r.raw(token)
	r.raw(`","user":`)
	st.mu.RLock()
	r.sessionUser(u)
	st.mu.RUnlock()
	r.raw(`}}`)
	*bp = r.b
	return writeJSON(c, http.StatusOK, r.b)
}

func (a *App) logoutHandler(c *echo.Context, s *session) error {
	a.repl.SubmitAsync(&Cmd{Kind: CmdLogout, User: s.user.ID, Token: hex.EncodeToString(s.hash[:])}, 0)
	return c.NoContent(http.StatusNoContent)
}

func (a *App) getMeHandler(c *echo.Context, s *session) error {
	r := newRenderer(a.st, make([]byte, 0, 512))
	a.st.mu.RLock()
	r.me(s.user)
	a.st.mu.RUnlock()
	return writeJSON(c, http.StatusOK, r.b)
}

func (a *App) updateMeHandler(c *echo.Context, s *session) error {
	var input updateMeRequest
	if err := c.Bind(&input); err != nil {
		return err
	}
	if (input.DisplayName == nil && input.Bio == nil && input.Region == nil) ||
		(input.DisplayName != nil && !validUserText(*input.DisplayName, 1, 100)) ||
		(input.Bio != nil && !validUserText(*input.Bio, 0, 500)) ||
		(input.Region != nil && !validRegion(*input.Region)) {
		return NewAPIError(http.StatusBadRequest, "invalid_profile")
	}
	res, err := a.repl.Submit(&Cmd{Kind: CmdUpdateMe, User: s.user.ID, P1: input.DisplayName, P2: input.Bio, P3: input.Region})
	if err != nil {
		return err
	}
	u := res.(*User)
	r := newRenderer(a.st, make([]byte, 0, 512))
	a.st.mu.RLock()
	r.me(u)
	a.st.mu.RUnlock()
	return writeJSON(c, http.StatusOK, r.b)
}

// JPEG の検査。ヘッダだけを読む。
func checkJPEG(data []byte, maxDim int) (ok bool, tooLarge bool) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return false, false
	}
	if cfg.Width > maxDim || cfg.Height > maxDim {
		return true, true
	}
	return true, false
}

// profileImageHeaderBytes は、本体を全部受け取る前に JPEG かどうかを見る長さ。
const profileImageHeaderBytes = 64 << 10

func (a *App) putProfileImageHandler(c *echo.Context, s *session) error {
	if c.Request().Header.Get("Content-Type") != "image/jpeg" {
		return NewAPIError(http.StatusUnsupportedMediaType, "unsupported_media_type")
	}
	body := c.Request().Body
	n := c.Request().ContentLength
	head := make([]byte, 0, profileImageHeaderBytes)
	buf := bytes.NewBuffer(head)
	if _, err := io.CopyN(buf, body, profileImageHeaderBytes); err != nil && err != io.EOF {
		return err
	}
	// 先頭だけで JPEG でないと分かれば、残りを受け取らずに断る（巨大な不正画像の連投に備える）
	ok, tooLarge := checkJPEG(buf.Bytes(), 512)
	if !ok {
		a.guard.Observe(c.RealIP(), failInvalidImage)
		c.Request().Close = true
		return NewAPIError(http.StatusBadRequest, "invalid_profile_image")
	}
	if tooLarge {
		c.Request().Close = true
		return NewAPIError(http.StatusBadRequest, "invalid_profile_image")
	}
	if n > int64(buf.Len()) {
		buf.Grow(int(n) - buf.Len())
	}
	if _, err := io.Copy(buf, body); err != nil {
		return err
	}
	data := buf.Bytes()
	imageID := newID()
	if err := a.imgs.Write(imageID, data); err != nil {
		return err
	}
	exp := currentImageExp(time.Now())
	sig := FullImageSignature(&s.user.Key, imageID, exp)
	_, err := a.repl.Submit(&Cmd{
		Kind: CmdSetProfileImage, User: s.user.ID, Img: imageID, ImgLen: uint32(len(data)),
		ImgCRC: crc32.ChecksumIEEE(data), ImgOrigin: a.cfg.Node, ImgSig: hex.EncodeToString(sig[:]), ImgExp: exp,
	})
	if err != nil {
		a.imgs.Remove(imageID)
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (a *App) deleteProfileImageHandler(c *echo.Context, s *session) error {
	if _, err := a.repl.Submit(&Cmd{Kind: CmdDeleteProfileImage, User: s.user.ID}); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (a *App) deleteAccountHandler(c *echo.Context, s *session) error {
	if _, err := a.repl.Submit(&Cmd{Kind: CmdDeleteAccount, User: s.user.ID}); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// --- ユーザーの検索と参照 ---

func (a *App) searchUsersHandler(c *echo.Context, s *session) error {
	var input struct {
		Q              string `query:"q"`
		BeforeUsername string `query:"beforeUsername"`
		BeforeID       string `query:"beforeId"`
	}
	if err := c.Bind(&input); err != nil {
		return err
	}
	if !validUserText(input.Q, 1, 100) {
		return NewAPIError(http.StatusBadRequest, "invalid_relationship")
	}
	if (input.BeforeUsername == "") != (input.BeforeID == "") {
		return NewAPIError(http.StatusBadRequest, "invalid_pagination")
	}
	query := normalizeSearchText(input.Q)
	st := a.st
	viewer := s.user
	bp := getBuf()
	defer putBuf(bp)
	r := newRenderer(a.st, (*bp)[:0])
	r.raw(`{"items":[`)
	count := 0
	hasMore := false
	st.mu.RLock()
	users := st.sortedUsers
	start := 0
	if input.BeforeUsername != "" {
		start = sort.Search(len(users), func(i int) bool {
			u := users[i]
			if u.Username != input.BeforeUsername {
				return u.Username > input.BeforeUsername
			}
			return u.IDStr > input.BeforeID
		})
	}
	for _, u := range users[start:] {
		if !u.Active || u == viewer {
			continue
		}
		if !strings.Contains(u.FoldUser, query) && !strings.Contains(u.FoldName, query) {
			continue
		}
		if viewer.isBlockedWith(u) {
			continue
		}
		if count == 50 {
			hasMore = true
			break
		}
		if count > 0 {
			r.raw(`,`)
		}
		bio := ""
		if u.Certified {
			bio = u.Bio
		}
		r.user(u, bio)
		count++
	}
	st.mu.RUnlock()
	r.raw(`],"hasMore":`)
	r.raw(boolStr(hasMore))
	r.raw(`}`)
	*bp = r.b
	return writeJSON(c, http.StatusOK, r.b)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (a *App) getUserHandler(c *echo.Context, s *session) error {
	st := a.st
	id, ok := parseUUIDParam(c.Param("userId"))
	if !ok {
		return NewAPIError(http.StatusNotFound, "relationship_not_found")
	}
	r := newRenderer(a.st, make([]byte, 0, 512))
	st.mu.RLock()
	u := st.users[id]
	if u == nil || !u.Active || s.user.isBlockedWith(u) {
		st.mu.RUnlock()
		return NewAPIError(http.StatusNotFound, "relationship_not_found")
	}
	bio := ""
	if u.Certified {
		bio = u.Bio
	} else if _, friend := s.user.Friends[u]; friend {
		bio = u.Bio
	}
	r.user(u, bio)
	st.mu.RUnlock()
	return writeJSON(c, http.StatusOK, r.b)
}

// --- 関係の一覧 ---

type relationRecord struct {
	id        ID
	idStr     string
	updatedAt int64
	user      *User
}

func (a *App) getRelationshipsHandler(c *echo.Context, s *session) error {
	var input struct {
		BeforeUpdatedAt *time.Time `query:"beforeUpdatedAt"`
		BeforeID        string     `query:"beforeId"`
	}
	if c.Request().URL.RawQuery != "" {
		if err := c.Bind(&input); err != nil {
			return err
		}
	}
	if (input.BeforeUpdatedAt == nil) != (input.BeforeID == "") {
		return NewAPIError(http.StatusBadRequest, "invalid_pagination")
	}
	kind := c.Param("kind")
	st := a.st
	u := s.user
	var before int64
	if input.BeforeUpdatedAt != nil {
		before = input.BeforeUpdatedAt.UnixMicro()
	}
	st.mu.RLock()
	unlocked := false
	unlock := func() {
		if !unlocked {
			unlocked = true
			st.mu.RUnlock()
		}
	}
	defer unlock()
	var records []relationRecord
	addRels := func(m map[*User]*Relation) {
		records = make([]relationRecord, 0, len(m))
		for other, rel := range m {
			if other.Active {
				records = append(records, relationRecord{id: rel.ID, updatedAt: rel.UpdatedAt, user: other})
			}
		}
	}
	addReqs := func(m map[*User]*FriendRequest) {
		records = make([]relationRecord, 0, len(m))
		for other, req := range m {
			if other.Active {
				records = append(records, relationRecord{id: req.ID, updatedAt: req.UpdatedAt, user: other})
			}
		}
	}
	status := kind
	switch kind {
	case "friends":
		addRels(u.Friends)
	case "following":
		addRels(u.Following)
	case "followers":
		addRels(u.Followers)
	case "incoming-requests":
		addReqs(u.Incoming)
		status = "pending"
	case "outgoing-requests":
		addReqs(u.Outgoing)
		status = "pending"
	default:
		return NewAPIError(http.StatusBadRequest, "invalid_relationship")
	}
	sort.Slice(records, func(i, j int) bool {
		x, y := &records[i], &records[j]
		if x.updatedAt != y.updatedAt {
			return x.updatedAt > y.updatedAt
		}
		return bytes.Compare(x.id[:], y.id[:]) > 0
	})
	bp := getBuf()
	defer putBuf(bp)
	r := newRenderer(a.st, (*bp)[:0])
	r.raw(`{"items":[`)
	count := 0
	hasMore := false
	for i := range records {
		rec := &records[i]
		if input.BeforeUpdatedAt != nil && !(rec.updatedAt < before || (rec.updatedAt == before && rec.id.String() < input.BeforeID)) {
			continue
		}
		if count == 50 {
			hasMore = true
			break
		}
		if count > 0 {
			r.raw(`,`)
		}
		r.raw(`{"relationId":"`)
		r.b = rec.id.appendTo(r.b)
		r.raw(`","user":`)
		bio := ""
		if rec.user.Certified || kind == "friends" {
			bio = rec.user.Bio
		}
		r.user(rec.user, bio)
		r.raw(`,"status":"`)
		r.raw(status)
		r.raw(`","updatedAt":`)
		r.b = appendTimeUS(r.b, rec.updatedAt)
		r.raw(`}`)
		count++
	}
	unlock()
	r.raw(`],"hasMore":`)
	r.raw(boolStr(hasMore))
	r.raw(`}`)
	*bp = r.b
	return writeJSON(c, http.StatusOK, r.b)
}

// --- 友達申請とフォローなど ---

func (a *App) createFriendRequestHandler(c *echo.Context, s *session) error {
	var input struct {
		RecipientID string `json:"recipientId"`
	}
	if err := c.Bind(&input); err != nil {
		return err
	}
	if input.RecipientID == "" || input.RecipientID == s.user.IDStr {
		return NewAPIError(http.StatusBadRequest, "invalid_relationship")
	}
	target, ok := parseUUIDParam(input.RecipientID)
	if !ok {
		return NewAPIError(http.StatusNotFound, "relationship_not_found")
	}
	res, err := a.repl.Submit(&Cmd{Kind: CmdFriendRequest, User: s.user.ID, Target: target, Obj: newID(), Obj2: newID()})
	if err != nil {
		return err
	}
	return writeFriendRequestResult(c, http.StatusCreated, res.(*friendRequestResult))
}

func writeFriendRequestResult(c *echo.Context, status int, res *friendRequestResult) error {
	b := make([]byte, 0, 128)
	b = append(b, `{"relationId":"`...)
	b = res.RelationID.appendTo(b)
	b = append(b, `","status":"`...)
	b = append(b, res.Status...)
	b = append(b, `","updatedAt":`...)
	b = appendTimeUS(b, res.UpdatedAt)
	b = append(b, '}')
	return writeJSON(c, status, b)
}

func (a *App) transitionFriendRequestHandler(c *echo.Context, s *session) error {
	var status string
	switch c.Param("action") {
	case "accept":
		status = "accepted"
	case "reject":
		status = "rejected"
	case "cancel":
		status = "cancelled"
	default:
		return NewAPIError(http.StatusForbidden, "relationship_action_forbidden")
	}
	id, ok := parseUUIDParam(c.Param("requestId"))
	if !ok {
		return NewAPIError(http.StatusNotFound, "relationship_not_found")
	}
	res, err := a.repl.Submit(&Cmd{Kind: CmdTransitionRequest, User: s.user.ID, Obj: id, Obj2: newID(), Str1: status})
	if err != nil {
		return err
	}
	return writeFriendRequestResult(c, http.StatusOK, res.(*friendRequestResult))
}

// relationTarget はパスの userId を読む。自分自身なら 400、形式が違えば 404（DB に行が無いのと同じ）。
func relationTarget(c *echo.Context, s *session) (ID, error) {
	param := c.Param("userId")
	if param == "" || param == s.user.IDStr {
		return ID{}, NewAPIError(http.StatusBadRequest, "invalid_relationship")
	}
	id, ok := parseUUIDParam(param)
	if !ok {
		return ID{}, NewAPIError(http.StatusNotFound, "relationship_not_found")
	}
	return id, nil
}

func (a *App) relationCommand(kind CmdKind) authedHandler {
	return func(c *echo.Context, s *session) error {
		target, err := relationTarget(c, s)
		if err != nil {
			return err
		}
		if _, err := a.repl.Submit(&Cmd{Kind: kind, User: s.user.ID, Target: target, Obj: newID()}); err != nil {
			return err
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func (a *App) deleteFriendshipHandler(c *echo.Context, s *session) error {
	return a.relationCommand(CmdDeleteFriendship)(c, s)
}
func (a *App) followHandler(c *echo.Context, s *session) error {
	return a.relationCommand(CmdFollow)(c, s)
}
func (a *App) unfollowHandler(c *echo.Context, s *session) error {
	return a.relationCommand(CmdUnfollow)(c, s)
}
func (a *App) blockHandler(c *echo.Context, s *session) error {
	return a.relationCommand(CmdBlock)(c, s)
}
func (a *App) unblockHandler(c *echo.Context, s *session) error {
	return a.relationCommand(CmdUnblock)(c, s)
}

// --- 管理者 ---

func (a *App) adminLoginHandler(c *echo.Context) error {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.Bind(&input); err != nil {
		return err
	}
	if !usernamePattern.MatchString(input.Username) {
		return NewAPIError(http.StatusUnauthorized, "invalid_credentials")
	}
	st := a.st
	st.mu.RLock()
	admin := st.admins[input.Username]
	st.mu.RUnlock()
	if admin == nil {
		return NewAPIError(http.StatusUnauthorized, "invalid_credentials")
	}
	hash := sha256.Sum256([]byte(input.Password))
	if admin.PasswordHash != hex.EncodeToString(hash[:]) {
		return NewAPIError(http.StatusUnauthorized, "invalid_credentials")
	}
	token := uuid.NewString()
	a.repl.SubmitAsync(&Cmd{Kind: CmdAdminLogin, Str1: admin.Username, Token: hashToken(token)}, 0)
	return c.JSON(http.StatusOK, map[string]string{"token": token, "administratorId": admin.IDStr})
}

func (a *App) adminLogoutHandler(c *echo.Context, hash [32]byte) error {
	a.repl.SubmitAsync(&Cmd{Kind: CmdAdminLogout, Token: hex.EncodeToString(hash[:])}, 0)
	return c.NoContent(http.StatusNoContent)
}

func (a *App) certifyUserHandler(c *echo.Context, hash [32]byte) error {
	id, ok := parseUUIDParam(c.Param("userId"))
	if !ok {
		return NewAPIError(http.StatusConflict, "certification_conflict")
	}
	if _, err := a.repl.Submit(&Cmd{Kind: CmdCertify, Target: id}); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (a *App) deleteUserCertificationHandler(c *echo.Context, hash [32]byte) error {
	return NewAPIError(http.StatusConflict, "certification_irreversible")
}

func (a *App) openPostingWindowHandler(c *echo.Context, hash [32]byte) error {
	var input struct {
		Region        string `json:"region"`
		LocalDate     string `json:"localDate"`
		OnTimeSeconds *int64 `json:"onTimeSeconds"`
	}
	if err := c.Bind(&input); err != nil {
		return err
	}
	localDate, err := time.Parse(time.DateOnly, input.LocalDate)
	if !validRegion(input.Region) || err != nil {
		return NewAPIError(http.StatusBadRequest, "invalid_posting_window")
	}
	seconds := int64(24 * 60 * 60)
	if input.OnTimeSeconds != nil {
		seconds = *input.OnTimeSeconds
	}
	res, err := a.repl.Submit(&Cmd{Kind: CmdOpenWindow, Obj: newID(), Str1: input.Region, Date: dateToDays(localDate), Secs: seconds})
	if err != nil {
		return err
	}
	w := res.(*Window)
	b := make([]byte, 0, 256)
	b = append(b, `{"id":"`...)
	b = append(b, w.IDStr...)
	b = append(b, `","region":"`...)
	b = append(b, w.Region...)
	b = append(b, `","localDate":"`...)
	b = append(b, w.LocalDateStr...)
	b = append(b, `","startsAt":`...)
	b = appendTimeUS(b, w.StartsAt)
	b = append(b, `,"onTimeDeadline":`...)
	b = appendTimeUS(b, w.OnTimeDeadline)
	b = append(b, '}')
	return writeJSON(c, http.StatusCreated, b)
}
