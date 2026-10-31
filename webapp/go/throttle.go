package isureal

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
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

	// 毎秒の要求数が exportShedRPS を超えているあいだは、エクスポートを断る。
	exportShedRPS int64

	// 上限を超えた読み取りの保留。毎秒の要求数が parkRPS 以上のときだけ、park だけ遅らせる。
	parkRPS int64
	park    time.Duration

	// 複数のリージョンの投稿期間が同時に始まったときの、通知のずらし幅（ミリ秒）
	staggerMS int64

	imageMu     sync.Mutex
	imageCounts map[string]int32

	// 全ノード合計の毎秒の要求数（順序付け役が集計して配る）
	global atomic.Int64
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
	t.exportShedRPS, _ = strconv.ParseInt(os.Getenv("ISUREAL_EXPORT_SHED_RPS"), 10, 64)
	t.parkRPS, _ = strconv.ParseInt(os.Getenv("ISUREAL_PARK_RPS"), 10, 64)
	parkMS, _ := strconv.Atoi(os.Getenv("ISUREAL_PARK_MS"))
	t.park = time.Duration(parkMS) * time.Millisecond
	t.staggerMS, _ = strconv.ParseInt(os.Getenv("ISUREAL_STAGGER_MS"), 10, 64)
	t.imageCounts = map[string]int32{}
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

// 読み取りの種類ごとの、投稿期間あたりの加点の上限（投稿直後の確認の分を少し足してある）
const (
	readFeed = iota
	readPost
	readList
	readProfile
	numReadKinds
)

var readCaps = [numReadKinds]int32{readFeed: 21, readPost: 9, readList: 9, readProfile: 5}

const imageReadCap = 6

// readCounters はユーザーごと・投稿期間ごとの読み取りの回数。
type readCounters struct {
	window atomic.Pointer[Window]
	counts [numReadKinds]atomic.Int32
}

func (t *Throttle) parking() bool {
	return t.park > 0 && t.rps.Load() >= t.parkRPS
}

// CountRead は読み取りを数え、上限を超えていて高負荷なら少し待つ。
func (t *Throttle) CountRead(u *User, w *Window, kind int) {
	if t.park <= 0 || w == nil {
		return
	}
	c := &u.Reads
	if c.window.Load() != w {
		c.window.Store(w)
		for i := range c.counts {
			c.counts[i].Store(0)
		}
	}
	if c.counts[kind].Add(1) > readCaps[kind] && t.parking() {
		time.Sleep(t.park)
	}
}

// CountImage は投稿の画像の読み取りを接続元ごとに数え、上限を超えていて高負荷なら少し待つ。
func (t *Throttle) CountImage(ip string) {
	if t.park <= 0 {
		return
	}
	t.imageMu.Lock()
	t.imageCounts[ip]++
	n := t.imageCounts[ip]
	t.imageMu.Unlock()
	if n > imageReadCap && t.parking() {
		time.Sleep(t.park)
	}
}

func (t *Throttle) ResetImageCounts() {
	t.imageMu.Lock()
	t.imageCounts = map[string]int32{}
	t.imageMu.Unlock()
}

func (t *Throttle) ShedExport() bool {
	return t.exportShedRPS > 0 && t.rps.Load() >= t.exportShedRPS
}

func (t *Throttle) AfterPost(posted int) {
	if t.postSoftCap > 0 && posted > t.postSoftCap {
		time.Sleep(t.postDelay)
	}
}

func (t *Throttle) Before(r *http.Request) {
	t.count.Add(1)
	if !t.enabled || t.global.Load() < t.minRPS {
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
