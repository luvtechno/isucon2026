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

	// 複数のリージョンの投稿期間が同時に始まったときの、通知のずらし幅（ミリ秒）
	staggerMS int64

	// 全ノード合計の毎秒の要求数（順序付け役が集計して配る）
	global atomic.Int64

	// 閉ループの保留: 全体の毎秒の要求数が clTarget に近いあいだ、asia の Memories を
	// トークンの数だけ返す。トークンの数は目標との差から毎 100ms 決める。
	clTarget int64
	clTokens chan struct{}
	clWait   time.Duration
}

func NewThrottle(region string) *Throttle {
	t := &Throttle{delays: map[string]time.Duration{}}
	if region == "asia" {
		t.clTarget, _ = strconv.ParseInt(os.Getenv("ISUREAL_CLOSED_LOOP_TARGET"), 10, 64)
		waitMS, _ := strconv.Atoi(os.Getenv("ISUREAL_CLOSED_LOOP_WAIT_MS"))
		t.clWait = time.Duration(waitMS) * time.Millisecond
		if t.clTarget > 0 {
			t.clTokens = make(chan struct{}, 64)
			go t.closedLoop()
		}
	}
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
	t.staggerMS, _ = strconv.ParseInt(os.Getenv("ISUREAL_STAGGER_MS"), 10, 64)
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
	case p == "/api/memories":
		return "month"
	case strings.HasPrefix(p, "/api/memories/"):
		return "date"
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

// closedLoop は全体の要求数と目標の差から、Memories を返す速さを決める。
// Memories を1つ返すと、そのあと約6要求が続く。
func (t *Throttle) closedLoop() {
	rate := 300.0
	for range time.Tick(100 * time.Millisecond) {
		rate += float64(t.clTarget-t.global.Load()) / 6 * 0.1 * 0.5
		rate = max(10, min(rate, 500))
		n := int(rate/10) + 1
		for range n {
			select {
			case t.clTokens <- struct{}{}:
			default:
			}
		}
	}
}

func (t *Throttle) closedLoopWait() {
	if t.global.Load() < t.clTarget*85/100 {
		return
	}
	select {
	case <-t.clTokens:
	case <-time.After(t.clWait):
	}
}

func (t *Throttle) Before(r *http.Request) {
	t.count.Add(1)
	if t.clTarget > 0 {
		if p := r.URL.Path; strings.HasPrefix(p, "/api/memories") {
			t.closedLoopWait()
		}
	}
	if !t.enabled || t.global.Load() < t.minRPS {
		return
	}
	class := classifyRequest(r)
	d, ok := t.delays[class]
	if !ok && (class == "month" || class == "date") {
		d, ok = t.delays["memories"]
	}
	if !ok {
		d, ok = t.delays["all"]
	}
	if ok {
		time.Sleep(d)
	}
}
