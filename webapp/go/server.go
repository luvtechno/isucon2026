package isureal

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

// App は1つのノードのアプリケーション。
type App struct {
	cfg      *Config
	pack     *Pack
	st       *State
	repl     *Replicator
	imgs     *ImageStore
	guard    *Guard
	echo     *echo.Echo
	throttle *Throttle

	publicIndex []byte
}

func extractClientIP(req *http.Request) string {
	if ip := req.Header.Get("X-Client-IP"); ip != "" {
		return ip
	}
	return echo.ExtractIPDirect()(req)
}

func NewApp(cfg *Config) *App {
	if !validRegion(cfg.Region) {
		panic("invalid server region: " + cfg.Region)
	}
	pack, err := OpenPack(cfg.PackDir)
	if err != nil {
		panic("failed to open pack: " + err.Error())
	}
	a := &App{cfg: cfg, pack: pack, st: NewState(pack)}
	a.imgs = NewImageStore(cfg, pack)
	a.repl = NewReplicator(a)
	a.guard = NewGuard(func(ip string, until int64) {
		a.repl.SubmitAsync(&Cmd{Kind: CmdBan, Str1: ip, T: until}, 0)
	})
	a.publicIndex, _ = os.ReadFile(filepath.Join(cfg.PublicDir, "index.html"))
	a.echo = a.newEcho()
	a.throttle = NewThrottle(cfg.Region)
	a.st.onWindowOpen = a.dispatchWindow
	return a
}

// staggerOrder は同時に始まった投稿期間の通知の順番。倍率の高いリージョンを先にする。
var staggerOrder = map[string]int64{"europe": 0, "america": 1, "asia": 2}

// dispatchWindow は投稿期間の開始を SSE の接続へ配る。
// 複数のリージョンの投稿期間が同時に始まったら、投稿が一度に集中しないように、リージョンごとに通知をずらす。
func (a *App) dispatchWindow(w *Window, subs []*sseSub) {
	go func() {
		stagger := a.throttle.staggerMS
		if stagger > 0 {
			time.Sleep(150 * time.Millisecond)
			st := a.st
			st.mu.RLock()
			together := false
			for region, other := range st.current {
				if region != w.Region && !other.Ended {
					if d := other.StartsAt - w.StartsAt; d > -1500_000 && d < 1500_000 {
						together = true
					}
				}
			}
			st.mu.RUnlock()
			if together {
				time.Sleep(time.Duration(stagger*staggerOrder[w.Region]) * time.Millisecond)
			}
		}
		a.throttle.ResetImageCounts()
		for _, sub := range subs {
			select {
			case sub.ch <- w:
			default:
			}
		}
	}()
}

func (a *App) Run() error {
	go a.pack.Warm(a.cfg.PackDir)
	a.repl.Start()
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/image/", a.imgs.internalHandler())
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		srv := &http.Server{Addr: ":" + strconv.Itoa(a.cfg.InternalPort), Handler: mux}
		log.Println(srv.ListenAndServe())
	}()
	var handler http.Handler = a
	if a.cfg.AccessLog != "" {
		handler = newAccessLogger(a, a.cfg.AccessLog)
	}
	srv := &http.Server{Addr: a.cfg.HTTPAddr, Handler: handler, ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 300 * time.Second}
	return srv.ListenAndServe()
}

const maxBodyBytes = 32 << 20

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/") {
		if !a.repl.ready.Load() {
			// 起動して複製が追いつくまでは受けられない。初期化だけは、追いつくのを待ってから受ける。
			if path == "/api/initialize" {
				for i := 0; i < 600 && !a.repl.ready.Load(); i++ {
					time.Sleep(100 * time.Millisecond)
				}
			}
			if !a.repl.ready.Load() {
				writeError(w, http.StatusServiceUnavailable, "service_unavailable")
				return
			}
		}
		if r.ContentLength > maxBodyBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "request_entity_too_large")
			return
		}
		a.throttle.Before(r)
		if r.Method == http.MethodGet && strings.HasPrefix(path, "/api/images/") {
			a.serveImage(w, r, path[len("/api/images/"):])
			return
		}
		a.echo.ServeHTTP(w, r)
		return
	}
	a.serveStatic(w, r)
}

func (a *App) serveStatic(w http.ResponseWriter, r *http.Request) {
	clean := filepath.Clean("/" + r.URL.Path)
	full := filepath.Join(a.cfg.PublicDir, clean)
	if st, err := os.Stat(full); err == nil && !st.IsDir() {
		http.ServeFile(w, r, full)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(a.publicIndex)
}

func writeError(w http.ResponseWriter, status int, code string) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	w.WriteHeader(status)
	var buf [128]byte
	b := append(buf[:0], `{"error":`...)
	b = appendJSONString(b, code)
	b = append(b, '}', '\n')
	w.Write(b)
}

func (a *App) isBanned(ip string) bool {
	bans := a.st.bans.Load()
	if len(*bans) == 0 {
		return false
	}
	until, ok := (*bans)[ip]
	return ok && time.Now().UnixMicro() < until
}

func (a *App) newEcho() *echo.Echo {
	e := echo.New()
	e.IPExtractor = extractClientIP
	e.Use(middleware.Recover())
	e.HTTPErrorHandler = APIErrorHandler
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if a.isBanned(c.RealIP()) {
				return NewAPIError(http.StatusForbidden, "ip_banned")
			}
			return next(c)
		}
	})
	auth := a.auth
	e.POST("/api/initialize", a.initializeHandler)
	e.POST("/api/auth/register", a.registerHandler)
	e.POST("/api/auth/login", a.loginHandler)
	e.POST("/api/auth/logout", auth(false, a.logoutHandler))
	e.GET("/api/users", auth(true, a.searchUsersHandler))
	e.GET("/api/users/:userId", auth(true, a.getUserHandler))
	e.GET("/api/me/export", auth(true, a.exportPostsHandler))
	e.GET("/api/me", auth(true, a.getMeHandler))
	e.PATCH("/api/me", auth(false, a.updateMeHandler))
	e.PUT("/api/me/profile-image", auth(false, a.putProfileImageHandler))
	e.DELETE("/api/me/profile-image", auth(false, a.deleteProfileImageHandler))
	e.POST("/api/me/account-deletion", auth(true, a.deleteAccountHandler))
	e.POST("/api/posts", auth(true, a.createPostHandler))
	e.GET("/api/posting-window/current", auth(true, a.getCurrentPostingWindowHandler))
	e.GET("/api/posts/me/current", auth(true, a.getMyCurrentPostHandler))
	e.GET("/api/posts/:postId", auth(true, a.getPostHandler))
	e.PATCH("/api/posts/:postId", auth(true, a.updatePostHandler))
	e.DELETE("/api/posts/:postId", auth(true, a.deletePostHandler))
	e.GET("/api/feeds/:feed", auth(true, a.getFeedHandler))
	e.GET("/api/memories", auth(true, a.getMemoriesHandler))
	e.GET("/api/memories/:date", auth(true, a.getMemoriesByDateHandler))
	e.POST("/api/posts/:postId/comments", auth(true, a.createCommentHandler))
	e.GET("/api/posts/:postId/comments", auth(true, a.getCommentsHandler))
	e.DELETE("/api/comments/:commentId", auth(true, a.deleteCommentHandler))
	e.POST("/api/posts/:postId/realmojis", auth(true, a.replaceRealMojiHandler))
	e.GET("/api/posts/:postId/realmojis", auth(true, a.getRealMojisHandler))
	e.DELETE("/api/realmojis/:realMojiId", auth(true, a.deleteRealMojiHandler))
	e.GET("/api/notifications", auth(true, a.getNotificationsHandler))
	e.POST("/api/friend-requests", auth(true, a.createFriendRequestHandler))
	e.POST("/api/friend-requests/:requestId/:action", auth(true, a.transitionFriendRequestHandler))
	e.GET("/api/relationships/:kind", auth(true, a.getRelationshipsHandler))
	e.DELETE("/api/friendships/:userId", auth(true, a.deleteFriendshipHandler))
	e.PUT("/api/follows/:userId", auth(true, a.followHandler))
	e.DELETE("/api/follows/:userId", auth(true, a.unfollowHandler))
	e.PUT("/api/blocks/:userId", auth(true, a.blockHandler))
	e.DELETE("/api/blocks/:userId", auth(true, a.unblockHandler))
	e.POST("/api/admin/auth/login", a.adminLoginHandler)
	e.POST("/api/admin/auth/logout", a.adminAuth(a.adminLogoutHandler))
	e.PUT("/api/admin/users/:userId/certification", a.adminAuth(a.certifyUserHandler))
	e.DELETE("/api/admin/users/:userId/certification", a.adminAuth(a.deleteUserCertificationHandler))
	e.POST("/api/admin/posting-window", a.adminAuth(a.openPostingWindowHandler))
	return e
}

type session struct {
	user *User
	hash [32]byte
}

type authedHandler func(c *echo.Context, s *session) error

const unknownTokenGrace = 500 * time.Millisecond

// auth は SessionMiddleware と RegionMiddleware に当たる。
func (a *App) auth(regionCheck bool, next authedHandler) echo.HandlerFunc {
	return func(c *echo.Context) error {
		header := c.Request().Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			return NewAPIError(http.StatusUnauthorized, "invalid_session")
		}
		hash := sha256.Sum256([]byte(header[len("Bearer "):]))
		a.repl.WaitFresh()
		st := a.st
		st.mu.RLock()
		u := st.sessions[hash]
		var region string
		var active bool
		if u != nil {
			region, active = u.Region, u.Active
		}
		st.mu.RUnlock()
		if u == nil {
			// 別のノードでログインした直後のトークンは、まだ届いていないことがある
			deadline := time.Now().Add(unknownTokenGrace)
			for u == nil && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
				st.mu.RLock()
				u = st.sessions[hash]
				if u != nil {
					region, active = u.Region, u.Active
				}
				st.mu.RUnlock()
			}
		}
		if u == nil || !active {
			return NewAPIError(http.StatusUnauthorized, "invalid_session")
		}
		if regionCheck && region != a.cfg.Region {
			return NewAPIError(http.StatusForbidden, "region_mismatch")
		}
		return next(c, &session{user: u, hash: hash})
	}
}

func (a *App) adminAuth(next func(c *echo.Context, hash [32]byte) error) echo.HandlerFunc {
	return func(c *echo.Context) error {
		header := c.Request().Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			return NewAPIError(http.StatusUnauthorized, "invalid_administrator_session")
		}
		hash := sha256.Sum256([]byte(header[len("Bearer "):]))
		st := a.st
		find := func() bool {
			st.mu.RLock()
			defer st.mu.RUnlock()
			return st.adminSessions[hash] != nil
		}
		ok := find()
		if !ok {
			deadline := time.Now().Add(unknownTokenGrace)
			for !ok && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
				ok = find()
			}
		}
		if !ok {
			return NewAPIError(http.StatusUnauthorized, "invalid_administrator_session")
		}
		return next(c, hash)
	}
}

var bufPool = sync.Pool{New: func() any { b := make([]byte, 0, 16<<10); return &b }}

func getBuf() *[]byte { return bufPool.Get().(*[]byte) }
func putBuf(b *[]byte) {
	if cap(*b) <= 4<<20 {
		*b = (*b)[:0]
		bufPool.Put(b)
	}
}

func writeJSON(c *echo.Context, status int, body []byte) error {
	w := c.Response()
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, err := w.Write(body)
	return err
}

// --- 画像 ---

var noStore = imageCacheControl()

func imageCacheControl() []string {
	if v := os.Getenv("ISUREAL_IMAGE_CC"); v != "" {
		return []string{v}
	}
	return []string{"no-store"}
}
var imageJPEG = []string{"image/jpeg"}

func (a *App) serveImage(w http.ResponseWriter, r *http.Request, idStr string) {
	h := w.Header()
	h["Cache-Control"] = noStore
	ip := extractClientIP(r)
	if a.isBanned(ip) {
		writeError(w, http.StatusForbidden, "ip_banned")
		return
	}
	expStr, sigStr, ok := parseImageQuery(r.URL.RawQuery)
	if !ok {
		writeError(w, http.StatusForbidden, "image_forbidden")
		return
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || strconv.FormatInt(exp, 10) != expStr || exp <= time.Now().Unix() {
		writeError(w, http.StatusForbidden, "image_forbidden")
		return
	}
	var sig [32]byte
	if len(sigStr) != 43 {
		writeError(w, http.StatusForbidden, "image_forbidden")
		return
	}
	if n, err := base64.RawURLEncoding.Strict().Decode(sig[:], []byte(sigStr)); err != nil || n != 32 {
		writeError(w, http.StatusForbidden, "image_forbidden")
		return
	}
	id, ok := ParseID(idStr)
	if !ok {
		writeError(w, http.StatusForbidden, "image_forbidden")
		return
	}

	var hist *HistImage
	var key, known [32]byte
	var knownExp int64
	var valid, found bool
	var origin uint8
	var length uint32
	lookup := func() {
		st := a.st
		st.mu.RLock()
		if live := st.images[id]; live != nil {
			if live.Owner.Active {
				found = true
				key = live.Owner.Key
				known, knownExp = live.Sig, live.SigExp
				origin, length = live.Origin, live.Len
				switch live.Kind {
				case ImageKindProfile:
					valid = live.Owner.HasProfile && live.Owner.ProfileImg == id
				case ImageKindPost:
					valid = !live.Post.Deleted
				case ImageKindRealmoji:
					rm := live.RM
					valid = !rm.Replaced && !rm.Deleted && !rm.Post.Deleted && rm.Post.Author.Active
				}
			}
			st.mu.RUnlock()
			return
		}
		st.mu.RUnlock()
		hist = a.pack.FindImage(id)
		if hist != nil {
			st.mu.RLock()
			owner := st.initUsers[hist.Owner]
			if owner.Active {
				found = true
				key = owner.Key
				knownExp = a.pack.SigExp
				switch hist.Kind {
				case ImageKindProfile:
					valid = owner.HasProfile && owner.ProfileImg == id
					known = a.pack.UserSigs[hist.Ref]
				case ImageKindPost:
					p := &a.pack.Posts[hist.Ref]
					valid = !p.DeletedInPack() && !st.histPostDeleted[hist.Ref]
					known = a.pack.PostSigs[hist.Ref]
				case ImageKindRealmoji:
					rm := &a.pack.RMs[hist.Ref]
					p := &a.pack.Posts[rm.Post]
					known = a.pack.RMSigs[hist.Ref]
					valid = rm.Replaced == 0 && !st.histRMDeleted[hist.Ref] && !p.DeletedInPack() && !st.histPostDeleted[rm.Post] && st.initUsers[p.Author].Active
				}
			}
			st.mu.RUnlock()
		}
	}
	lookup()
	if !found || !valid {
		// 手元の複製が遅れていると、作られたばかりの画像が見えない。追いついてから引き直す。
		a.repl.WaitFresh()
		found, valid, hist = false, false, nil
		lookup()
	}
	if !found {
		writeError(w, http.StatusForbidden, "image_forbidden")
		return
	}
	var expected [32]byte
	if exp == knownExp {
		expected = known
	} else {
		expected = cachedSignature(&key, id, exp)
	}
	if expected != sig {
		a.guard.Observe(ip, failInvalidSignature)
		writeError(w, http.StatusForbidden, "image_forbidden")
		return
	}
	if !valid {
		writeError(w, http.StatusForbidden, "image_forbidden")
		return
	}
	if hist != nil && a.throttle.ShedHistImage() {
		writeError(w, http.StatusInternalServerError, "image_unavailable")
		return
	}
	h["Content-Type"] = imageJPEG
	if hist != nil {
		bp := getBuf()
		data, err := a.imgs.ReadHist(hist, (*bp)[:0])
		if err != nil {
			putBuf(bp)
			delete(h, "Content-Type")
			writeError(w, http.StatusInternalServerError, "image_unavailable")
			return
		}
		h["Content-Length"] = []string{strconv.Itoa(len(data))}
		w.Write(data)
		*bp = data
		putBuf(bp)
		return
	}
	a.throttle.CountImage(ip)
	f, err := a.imgs.Open(id, origin)
	if err != nil {
		log.Printf("failed to open image %s: %v", idStr, err)
		delete(h, "Content-Type")
		writeError(w, http.StatusInternalServerError, "image_unavailable")
		return
	}
	defer f.Close()
	h["Content-Length"] = []string{strconv.FormatUint(uint64(length), 10)}
	io.Copy(w, f)
}

// parseImageQuery は exp と sig がそれぞれ1回ずつあることを確かめて取り出す。
func parseImageQuery(q string) (string, string, bool) {
	// ふつうの URL の形なら速く読む
	if strings.HasPrefix(q, "exp=") {
		if i := strings.Index(q, "&sig="); i > 0 {
			exp, sig := q[4:i], q[i+5:]
			if !strings.ContainsAny(exp, "&;%+") && !strings.ContainsAny(sig, "&;%+") {
				return exp, sig, true
			}
		}
	}
	query, err := url.ParseQuery(q)
	if err != nil || len(query["exp"]) != 1 || len(query["sig"]) != 1 {
		return "", "", false
	}
	return query["exp"][0], query["sig"][0], true
}

// --- 初期化 ---

type initializeResponse struct {
	Language string `json:"language"`
	Agent    string `json:"agent"`
	Harness  string `json:"harness"`
	Model    string `json:"model"`
}

func (a *App) initializeHandler(c *echo.Context) error {
	if err := a.repl.Initialize(); err != nil {
		return err
	}
	a.guard.Reset()
	return c.JSON(http.StatusOK, initializeResponse{Language: "go", Agent: "claude-code", Harness: "claude-code", Model: "claude-opus-5-5"})
}

var _ = json.Marshal
var _ = fmt.Sprint
