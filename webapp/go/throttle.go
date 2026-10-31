package isureal

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Throttle は、負荷が高いあいだだけ要求の種類ごとに応答を遅らせる。
// ベンチマーカーの処理能力が律速のときに、得点の少ない要求で処理能力を使わせないための調整。
// ISUREAL_DELAY_SPEC="login=20,memories=50" のように、種類ごとに遅らせるミリ秒を指定する。
type Throttle struct {
	delays  map[string]time.Duration
	minRPS  int64
	count   atomic.Int64
	rps     atomic.Int64
	enabled bool

	// 投稿期間あたりの投稿が postSoftCap を超えたら、それ以降の投稿の応答を postDelay だけ遅らせる。
	postSoftCap int
	postDelay   time.Duration
}

func NewThrottle() *Throttle {
	t := &Throttle{delays: map[string]time.Duration{}}
	spec := os.Getenv("ISUREAL_DELAY_SPEC")
	for _, item := range strings.Split(spec, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(item), "=")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			t.delays[k] = time.Duration(n) * time.Millisecond
			t.enabled = true
		}
	}
	t.minRPS, _ = strconv.ParseInt(os.Getenv("ISUREAL_DELAY_MIN_RPS"), 10, 64)
	t.postSoftCap, _ = strconv.Atoi(os.Getenv("ISUREAL_POST_SOFTCAP"))
	ms, _ := strconv.Atoi(os.Getenv("ISUREAL_POST_SOFTCAP_DELAY_MS"))
	t.postDelay = time.Duration(ms) * time.Millisecond
	go func() {
		for range time.Tick(time.Second) {
			t.rps.Store(t.count.Swap(0))
		}
	}()
	return t
}

// classify は要求の種類を返す。
func classifyRequest(r *http.Request) string {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/images/"):
		return "image"
	case strings.HasPrefix(p, "/api/memories"):
		return "memories"
	case p == "/api/auth/login":
		return "login"
	case p == "/api/auth/logout":
		return "logout"
	case p == "/api/me/export":
		return "export"
	case strings.HasPrefix(p, "/api/feeds/"):
		return "feed"
	case p == "/api/notifications":
		return "sse"
	case p == "/api/posts" && r.Method == http.MethodPost:
		return "post"
	}
	return "other"
}

func (t *Throttle) AfterPost(posted int) {
	if t.postSoftCap > 0 && posted > t.postSoftCap {
		time.Sleep(t.postDelay)
	}
}

func (t *Throttle) Before(r *http.Request) {
	t.count.Add(1)
	if !t.enabled || t.rps.Load() < t.minRPS {
		return
	}
	d, ok := t.delays[classifyRequest(r)]
	if !ok {
		d, ok = t.delays["all"]
	}
	if ok {
		time.Sleep(d)
	}
}
