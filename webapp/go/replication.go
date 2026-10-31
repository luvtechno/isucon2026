package isureal

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// フレームの形式: 長さ(4バイト、種別を含む) + 種別(1バイト) + 本文
const (
	frameHello    = 1 // replica → central: JSON{node, epoch, from}
	frameSubmit   = 2 // replica → central: Cmd の JSON
	frameEntry    = 3 // central → replica: seq(8) ts(8) Cmd の JSON
	frameTick     = 4 // central → replica: ts(8) durable(8)
	frameReply    = 5 // central → replica: req(8) status(4) code
	frameReset    = 6 // central → replica: epoch(8) req(8) origin(1)
	frameResetAck = 7 // replica → central: epoch(8)
	frameCaughtUp = 8 // central → replica: seq(8)
	frameInit     = 9 // replica → central: req(8)
)

func frameOf(typ byte, body ...[]byte) []byte {
	n := 1
	for _, b := range body {
		n += len(b)
	}
	f := make([]byte, 4, 4+n)
	binary.BigEndian.PutUint32(f, uint32(n))
	f = append(f, typ)
	for _, b := range body {
		f = append(f, b...)
	}
	return f
}

func readFrame(r *bufio.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:4])
	if n == 0 || n > 64<<20 {
		return 0, nil, fmt.Errorf("bad frame length %d", n)
	}
	body := make([]byte, n-1)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return hdr[4], body, nil
}

func le64(v uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	return b
}

// commitWait は、書き込みの応答を返すまでに順序付けの時刻から空ける時間。
// 他のリージョンの複製へ届くまでの片道（約50ms）より長くして、応答を受け取ったあとの
// 読み取りが、どのリージョンでも結果を反映しているようにする。
var commitWaitUS = int64(60_000)

// Replicator は1つのノードから見たレプリケーション。
type Replicator struct {
	app  *App
	node uint8
	st   *State

	// 手元の状態がどの時刻まで追いついているか（読み取りの鮮度の確認に使う）
	freshTs atomic.Int64
	durable atomic.Uint64
	applied atomic.Uint64
	epoch   atomic.Uint64
	ready   atomic.Bool

	// central
	isCentral bool
	seqMu     sync.Mutex
	seq       uint64
	lastTs    int64
	frames    [][]byte
	peers     map[*peer]struct{}
	logMu     sync.Mutex
	logBuf    []byte
	logSeq    uint64
	logCond   *sync.Cond
	logFile   *os.File
	dataDir   string
	resetAcks chan uint64
	initMu    sync.Mutex

	// replica
	centralAddr string
	connMu      sync.Mutex
	conn        net.Conn
	sendMu      sync.Mutex
	sendBuf     []byte
	sendCond    *sync.Cond
	pendingMu   sync.Mutex
	pending     map[uint64]chan *submitResult
	reqSeq      atomic.Uint64
}

type submitResult struct {
	res any
	err error
	ts  int64
	seq uint64
}

type peer struct {
	conn net.Conn
	node uint8
	mu   sync.Mutex
	buf  []byte
	cond *sync.Cond
	dead bool
}

func (p *peer) enqueue(f []byte) {
	p.mu.Lock()
	p.buf = append(p.buf, f...)
	p.cond.Signal()
	p.mu.Unlock()
}

func (p *peer) writer() {
	var out []byte
	for {
		p.mu.Lock()
		for len(p.buf) == 0 && !p.dead {
			p.cond.Wait()
		}
		if p.dead {
			p.mu.Unlock()
			return
		}
		out, p.buf = p.buf, out[:0]
		p.mu.Unlock()
		if _, err := p.conn.Write(out); err != nil {
			p.close()
			return
		}
	}
}

func (p *peer) close() {
	p.mu.Lock()
	p.dead = true
	p.cond.Signal()
	p.mu.Unlock()
	p.conn.Close()
}

func NewReplicator(app *App) *Replicator {
	r := &Replicator{
		app: app, node: app.cfg.Node, st: app.st,
		peers: map[*peer]struct{}{}, pending: map[uint64]chan *submitResult{},
		isCentral: app.cfg.CentralAddr == "", centralAddr: app.cfg.CentralAddr,
		dataDir: app.cfg.DataDir, resetAcks: make(chan uint64, 16),
	}
	r.logCond = sync.NewCond(&r.logMu)
	r.sendCond = sync.NewCond(&r.sendMu)
	return r
}

func (r *Replicator) Start() {
	if r.isCentral {
		if err := r.recoverLog(); err != nil {
			log.Fatalf("failed to recover log: %v", err)
		}
		r.ready.Store(true)
		go r.logFlusher()
		go r.ticker()
		go r.listen()
		return
	}
	go r.replicaLoop()
	go r.replicaSender()
}

// --- central ---

func (r *Replicator) logPath(epoch uint64) string {
	return filepath.Join(r.dataDir, fmt.Sprintf("log-%d.bin", epoch))
}

func (r *Replicator) recoverLog() error {
	if err := os.MkdirAll(r.dataDir, 0o755); err != nil {
		return err
	}
	var epoch uint64
	entries, err := os.ReadDir(r.dataDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if n, ok := strings.CutPrefix(e.Name(), "log-"); ok {
			if v, err := strconv.ParseUint(strings.TrimSuffix(n, ".bin"), 10, 64); err == nil && v > epoch {
				epoch = v
			}
		}
	}
	if epoch == 0 {
		epoch = 1
	}
	r.epoch.Store(epoch)
	path := r.logPath(epoch)
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// 書きかけの末尾は捨てて、読めたところまでを適用する
	good := 0
	for good+5 <= len(raw) {
		n := int(binary.BigEndian.Uint32(raw[good:]))
		if good+4+n > len(raw) {
			break
		}
		frame := raw[good : good+4+n]
		body := frame[5:]
		seq := binary.LittleEndian.Uint64(body[:8])
		ts := int64(binary.LittleEndian.Uint64(body[8:16]))
		var c Cmd
		if err := json.Unmarshal(body[16:], &c); err != nil {
			break
		}
		r.st.mu.Lock()
		_, _ = r.st.apply(&c, ts)
		r.st.mu.Unlock()
		r.seq = seq
		r.lastTs = ts
		r.frames = append(r.frames, append([]byte(nil), frame...))
		good += 4 + n
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(good)); err != nil {
		return err
	}
	if _, err := f.Seek(int64(good), io.SeekStart); err != nil {
		return err
	}
	r.logFile = f
	r.logSeq = r.seq
	r.durable.Store(r.seq)
	r.applied.Store(r.seq)
	log.Printf("recovered log epoch=%d seq=%d bytes=%d", epoch, r.seq, good)
	return nil
}

func (r *Replicator) logFlusher() {
	var out []byte
	for {
		r.logMu.Lock()
		for len(r.logBuf) == 0 {
			r.logCond.Wait()
		}
		out, r.logBuf = r.logBuf, out[:0]
		seq := r.logSeq
		f := r.logFile
		r.logMu.Unlock()
		if _, err := f.Write(out); err != nil {
			log.Printf("log write failed: %v", err)
		}
		if err := f.Sync(); err != nil {
			log.Printf("log sync failed: %v", err)
		}
		r.durable.Store(seq)
	}
}

func (r *Replicator) ticker() {
	for {
		time.Sleep(3 * time.Millisecond)
		r.seqMu.Lock()
		now := time.Now().UnixMicro()
		if now <= r.lastTs {
			now = r.lastTs + 1
		}
		r.lastTs = now
		r.freshTs.Store(now)
		var body [16]byte
		binary.LittleEndian.PutUint64(body[:8], uint64(now))
		binary.LittleEndian.PutUint64(body[8:], r.durable.Load())
		f := frameOf(frameTick, body[:])
		for p := range r.peers {
			p.enqueue(f)
		}
		r.seqMu.Unlock()
	}
}

// execLocal はコマンドを順序付けて適用する（central の上でだけ呼ぶ）。
func (r *Replicator) execLocal(c *Cmd) (any, error, int64, uint64) {
	payload, err := json.Marshal(c)
	if err != nil {
		return nil, err, 0, 0
	}
	r.seqMu.Lock()
	defer r.seqMu.Unlock()
	now := time.Now().UnixMicro()
	if now <= r.lastTs {
		now = r.lastTs + 1
	}
	r.st.mu.Lock()
	res, aerr := r.st.apply(c, now)
	r.st.mu.Unlock()
	if aerr != nil {
		return nil, aerr, now, 0
	}
	r.lastTs = now
	r.freshTs.Store(now)
	r.seq++
	seq := r.seq
	var hdr [16]byte
	binary.LittleEndian.PutUint64(hdr[:8], seq)
	binary.LittleEndian.PutUint64(hdr[8:], uint64(now))
	f := frameOf(frameEntry, hdr[:], payload)
	r.frames = append(r.frames, f)
	r.applied.Store(seq)
	for p := range r.peers {
		p.enqueue(f)
	}
	r.logMu.Lock()
	r.logBuf = append(r.logBuf, f...)
	r.logSeq = seq
	r.logCond.Signal()
	r.logMu.Unlock()
	return res, nil, now, seq
}

func (r *Replicator) listen() {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(r.app.cfg.ReplPort))
	if err != nil {
		log.Fatalf("failed to listen for replication: %v", err)
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept failed: %v", err)
			continue
		}
		go r.servePeer(conn)
	}
}

type helloMsg struct {
	Node  uint8  `json:"node"`
	Epoch uint64 `json:"epoch"`
	From  uint64 `json:"from"`
}

func (r *Replicator) servePeer(conn net.Conn) {
	defer conn.Close()
	tuneConn(conn)
	br := bufio.NewReaderSize(conn, 256<<10)
	typ, body, err := readFrame(br)
	if err != nil || typ != frameHello {
		return
	}
	var hello helloMsg
	if err := json.Unmarshal(body, &hello); err != nil {
		return
	}
	p := &peer{conn: conn, node: hello.Node}
	p.cond = sync.NewCond(&p.mu)
	go p.writer()
	defer p.close()

	r.seqMu.Lock()
	epoch := r.epoch.Load()
	from := hello.From
	if hello.Epoch != epoch || from > r.seq {
		// 初期化をまたいだか、手元のログより先へ進んでいる複製は、最初からやり直させる
		p.enqueue(frameOf(frameReset, le64(epoch), le64(0), []byte{0}))
		from = 0
	}
	for _, f := range r.frames[from:] {
		p.enqueue(f)
	}
	p.enqueue(frameOf(frameCaughtUp, le64(r.seq)))
	r.peers[p] = struct{}{}
	r.seqMu.Unlock()
	log.Printf("peer connected: node=%d epoch=%d from=%d", hello.Node, hello.Epoch, from)
	defer func() {
		r.seqMu.Lock()
		delete(r.peers, p)
		r.seqMu.Unlock()
		log.Printf("peer disconnected: node=%d", hello.Node)
	}()

	for {
		typ, body, err := readFrame(br)
		if err != nil {
			return
		}
		switch typ {
		case frameSubmit:
			var c Cmd
			if err := json.Unmarshal(body, &c); err != nil {
				log.Printf("bad submit: %v", err)
				continue
			}
			_, aerr, _, _ := r.execLocal(&c)
			if aerr != nil && c.Req != 0 {
				var ae *APIError
				status, code := http.StatusInternalServerError, aerr.Error()
				if errors.As(aerr, &ae) {
					status, code = ae.Status, ae.Code
				}
				var hdr [12]byte
				binary.LittleEndian.PutUint64(hdr[:8], c.Req)
				binary.LittleEndian.PutUint32(hdr[8:], uint32(status))
				p.enqueue(frameOf(frameReply, hdr[:], []byte(code)))
			}
		case frameResetAck:
			select {
			case r.resetAcks <- binary.LittleEndian.Uint64(body):
			default:
			}
		case frameInit:
			req := binary.LittleEndian.Uint64(body)
			go func(origin uint8) {
				r.resetAll()
				var hdr [12]byte
				binary.LittleEndian.PutUint64(hdr[:8], req)
				binary.LittleEndian.PutUint32(hdr[8:], 200)
				p.enqueue(frameOf(frameReply, hdr[:], []byte("ok")))
			}(hello.Node)
		}
	}
}

// resetAll は全ノードを初期状態へ戻す。つながっている複製の完了を待ってから返る。
func (r *Replicator) resetAll() {
	r.initMu.Lock()
	defer r.initMu.Unlock()
	for len(r.resetAcks) > 0 {
		<-r.resetAcks
	}
	r.seqMu.Lock()
	epoch := r.epoch.Load() + 1
	old := r.logFile
	f, err := os.OpenFile(r.logPath(epoch), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		r.seqMu.Unlock()
		log.Printf("failed to open new log: %v", err)
		return
	}
	r.logMu.Lock()
	r.logBuf = nil
	r.logFile = f
	r.logSeq = 0
	r.logMu.Unlock()
	r.st.mu.Lock()
	r.st.reset()
	r.st.mu.Unlock()
	r.epoch.Store(epoch)
	r.seq = 0
	r.frames = nil
	r.applied.Store(0)
	r.durable.Store(0)
	waiting := len(r.peers)
	f0 := frameOf(frameReset, le64(epoch), le64(0), []byte{0})
	for p := range r.peers {
		p.enqueue(f0)
	}
	r.seqMu.Unlock()
	if old != nil {
		old.Close()
	}
	os.Remove(r.logPath(epoch - 1))
	r.app.imgs.Reset()
	deadline := time.After(10 * time.Second)
	for waiting > 0 {
		select {
		case e := <-r.resetAcks:
			if e == epoch {
				waiting--
			}
		case <-deadline:
			log.Printf("reset: %d peers did not ack", waiting)
			return
		}
	}
}

// --- replica ---

func tuneConn(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(5 * time.Second)
		tc.SetReadBuffer(4 << 20)
		tc.SetWriteBuffer(4 << 20)
	}
}

func (r *Replicator) replicaLoop() {
	for {
		if err := r.replicaSession(); err != nil {
			log.Printf("replication session ended: %v", err)
		}
		r.connMu.Lock()
		r.conn = nil
		r.connMu.Unlock()
		r.failPending()
		time.Sleep(200 * time.Millisecond)
	}
}

func (r *Replicator) failPending() {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	for id, ch := range r.pending {
		ch <- &submitResult{err: NewAPIError(http.StatusServiceUnavailable, "service_unavailable")}
		delete(r.pending, id)
	}
}

func (r *Replicator) replicaSession() error {
	conn, err := net.DialTimeout("tcp", r.centralAddr, 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	tuneConn(conn)
	hello, _ := json.Marshal(helloMsg{Node: r.node, Epoch: r.epoch.Load(), From: r.applied.Load()})
	if _, err := conn.Write(frameOf(frameHello, hello)); err != nil {
		return err
	}
	r.connMu.Lock()
	r.conn = conn
	r.connMu.Unlock()
	r.sendCond.L.Lock()
	r.sendCond.Broadcast()
	r.sendCond.L.Unlock()
	log.Printf("connected to central %s", r.centralAddr)

	br := bufio.NewReaderSize(conn, 1<<20)
	for {
		typ, body, err := readFrame(br)
		if err != nil {
			return err
		}
		switch typ {
		case frameEntry:
			seq := binary.LittleEndian.Uint64(body[:8])
			ts := int64(binary.LittleEndian.Uint64(body[8:16]))
			var c Cmd
			if err := json.Unmarshal(body[16:], &c); err != nil {
				return err
			}
			r.applyEntry(&c, seq, ts)
		case frameTick:
			ts := int64(binary.LittleEndian.Uint64(body[:8]))
			r.durable.Store(binary.LittleEndian.Uint64(body[8:]))
			r.freshTs.Store(ts)
		case frameReply:
			req := binary.LittleEndian.Uint64(body[:8])
			status := int(binary.LittleEndian.Uint32(body[8:12]))
			code := string(body[12:])
			r.pendingMu.Lock()
			ch := r.pending[req]
			delete(r.pending, req)
			r.pendingMu.Unlock()
			if ch != nil {
				if status == 200 {
					ch <- &submitResult{}
				} else {
					ch <- &submitResult{err: NewAPIError(status, code)}
				}
			}
		case frameReset:
			epoch := binary.LittleEndian.Uint64(body[:8])
			r.st.mu.Lock()
			r.st.reset()
			r.st.mu.Unlock()
			r.app.imgs.Reset()
			r.app.guard.Reset()
			r.epoch.Store(epoch)
			r.applied.Store(0)
			r.durable.Store(0)
			r.queueFrame(frameOf(frameResetAck, le64(epoch)))
		case frameCaughtUp:
			r.ready.Store(true)
			log.Printf("caught up: seq=%d", binary.LittleEndian.Uint64(body))
		}
	}
}

func (r *Replicator) applyEntry(c *Cmd, seq uint64, ts int64) {
	if c.isAsync() && c.Origin == r.node && r.ready.Load() {
		// 手元で適用済み
		r.applied.Store(seq)
		r.freshTs.Store(ts)
		return
	}
	r.st.mu.Lock()
	res, err := r.st.apply(c, ts)
	r.st.mu.Unlock()
	r.applied.Store(seq)
	r.freshTs.Store(ts)
	if err != nil {
		log.Printf("apply failed on replica: kind=%d err=%v", c.Kind, err)
	}
	if c.Req != 0 && c.Origin == r.node {
		r.pendingMu.Lock()
		ch := r.pending[c.Req]
		delete(r.pending, c.Req)
		r.pendingMu.Unlock()
		if ch != nil {
			ch <- &submitResult{res: res, err: err, ts: ts, seq: seq}
		}
	}
}

func (r *Replicator) queueFrame(f []byte) {
	r.sendMu.Lock()
	r.sendBuf = append(r.sendBuf, f...)
	r.sendCond.Signal()
	r.sendMu.Unlock()
}

func (r *Replicator) replicaSender() {
	var out []byte
	for {
		r.sendMu.Lock()
		for len(r.sendBuf) == 0 {
			r.sendCond.Wait()
		}
		r.connMu.Lock()
		conn := r.conn
		r.connMu.Unlock()
		if conn == nil {
			r.sendMu.Unlock()
			time.Sleep(50 * time.Millisecond)
			continue
		}
		out, r.sendBuf = r.sendBuf, out[:0]
		r.sendMu.Unlock()
		if _, err := conn.Write(out); err != nil {
			log.Printf("send failed: %v", err)
			conn.Close()
		}
	}
}

// Submit は同期のコマンドを順序付け役へ出し、適用の結果を受け取る。
// 応答は、他のノードへ行き渡る時間と永続化を待ってから返る。
func (r *Replicator) Submit(c *Cmd) (any, error) {
	c.Origin = r.node
	if r.isCentral {
		res, err, ts, seq := r.execLocal(c)
		if err != nil {
			return nil, err
		}
		r.waitCommit(ts, seq)
		return res, nil
	}
	r.connMu.Lock()
	connected := r.conn != nil
	r.connMu.Unlock()
	if !connected {
		return nil, NewAPIError(http.StatusServiceUnavailable, "service_unavailable")
	}
	id := r.reqSeq.Add(1)
	c.Req = id
	payload, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	ch := make(chan *submitResult, 1)
	r.pendingMu.Lock()
	r.pending[id] = ch
	r.pendingMu.Unlock()
	r.queueFrame(frameOf(frameSubmit, payload))
	select {
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		r.waitCommit(res.ts, res.seq)
		return res.res, nil
	case <-time.After(20 * time.Second):
		r.pendingMu.Lock()
		delete(r.pending, id)
		r.pendingMu.Unlock()
		return nil, NewAPIError(http.StatusServiceUnavailable, "service_unavailable")
	}
}

// SubmitAsync は手元で適用してから順序付け役へ送る（応答を待たない）。
func (r *Replicator) SubmitAsync(c *Cmd, ts int64) {
	c.Origin = r.node
	if r.isCentral {
		r.execLocal(c)
		return
	}
	r.st.mu.Lock()
	r.st.apply(c, ts)
	r.st.mu.Unlock()
	payload, err := json.Marshal(c)
	if err != nil {
		return
	}
	r.queueFrame(frameOf(frameSubmit, payload))
}

func (r *Replicator) waitCommit(ts int64, seq uint64) {
	for {
		wait := ts + commitWaitUS - time.Now().UnixMicro()
		if wait <= 0 {
			break
		}
		time.Sleep(time.Duration(wait) * time.Microsecond)
	}
	for i := 0; r.durable.Load() < seq && r.epochStable(seq) && i < 2000; i++ {
		time.Sleep(time.Millisecond)
	}
}

func (r *Replicator) epochStable(seq uint64) bool {
	// 初期化で seq が巻き戻ったら、待っても来ない
	return r.applied.Load() >= seq
}

// WaitFresh は読み取りの前に、手元の複製が十分に新しいことを確かめる。
// 書き込みの応答は順序付けの時刻から commitWait のあとなので、複製が
// 「いま - commitWait」まで追いついていれば、応答済みの書き込みはすべて見える。
func (r *Replicator) WaitFresh() {
	if r.isCentral {
		return
	}
	limit := time.Now().UnixMicro() - commitWaitUS
	if r.freshTs.Load() >= limit {
		return
	}
	for i := 0; i < 2000; i++ {
		time.Sleep(time.Millisecond)
		if r.freshTs.Load() >= limit {
			return
		}
	}
}

// Initialize は全ノードを初期状態へ戻す。
func (r *Replicator) Initialize() error {
	if r.isCentral {
		r.resetAll()
		return nil
	}
	r.connMu.Lock()
	connected := r.conn != nil
	r.connMu.Unlock()
	if !connected {
		return NewAPIError(http.StatusServiceUnavailable, "service_unavailable")
	}
	id := r.reqSeq.Add(1)
	ch := make(chan *submitResult, 1)
	r.pendingMu.Lock()
	r.pending[id] = ch
	r.pendingMu.Unlock()
	r.queueFrame(frameOf(frameInit, le64(id)))
	select {
	case res := <-ch:
		return res.err
	case <-time.After(30 * time.Second):
		return NewAPIError(http.StatusServiceUnavailable, "service_unavailable")
	}
}
