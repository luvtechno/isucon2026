package isureal

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// ImageStore は、この期間に作られた画像の本体を手元のファイルに持つ。
// 別のリージョンで作られた画像は、最初に要求されたときに持ち主のノードから取ってくる。
type ImageStore struct {
	base     string
	cur      atomic.Pointer[string]
	client   *http.Client
	cfg      *Config
	pack     *Pack
	flightMu sync.Mutex
	flights  map[ID]*imageFlight
	trashSeq atomic.Int64
}

type imageFlight struct {
	done chan struct{}
	err  error
}

func NewImageStore(cfg *Config, pack *Pack) *ImageStore {
	s := &ImageStore{
		base: filepath.Join(cfg.DataDir, "images"), cfg: cfg, pack: pack,
		flights: map[ID]*imageFlight{},
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns: 256, MaxIdleConnsPerHost: 64, IdleConnTimeout: 300 * time.Second,
				DisableCompression: true, ReadBufferSize: 256 << 10, WriteBufferSize: 64 << 10,
				DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 15 * time.Second}).DialContext,
			},
		},
	}
	cur := filepath.Join(s.base, "cur")
	if err := os.MkdirAll(cur, 0o755); err != nil {
		log.Fatalf("failed to prepare image dir: %v", err)
	}
	s.cur.Store(&cur)
	// 前回の初期化で消しきれなかったものを片付ける
	if entries, err := os.ReadDir(s.base); err == nil {
		for _, e := range entries {
			if e.Name() != "cur" {
				go os.RemoveAll(filepath.Join(s.base, e.Name()))
			}
		}
	}
	return s
}

func (s *ImageStore) path(id ID) string {
	var buf [128]byte
	b := append(buf[:0], *s.cur.Load()...)
	b = append(b, '/', hexDigits[id[0]>>4], hexDigits[id[0]&0xf], '/')
	b = id.appendTo(b)
	return string(b)
}

// Reset は手元の画像を全部捨てる（初期化）。
func (s *ImageStore) Reset() {
	cur := *s.cur.Load()
	trash := filepath.Join(s.base, fmt.Sprintf("trash-%d-%d", time.Now().UnixNano(), s.trashSeq.Add(1)))
	if err := os.Rename(cur, trash); err != nil && !os.IsNotExist(err) {
		log.Printf("failed to rotate images: %v", err)
	}
	if err := os.MkdirAll(cur, 0o755); err != nil {
		log.Printf("failed to recreate images: %v", err)
	}
	go os.RemoveAll(trash)
}

func (s *ImageStore) Write(id ID, data []byte) error {
	path := s.path(id)
	err := os.WriteFile(path, data, 0o644)
	if err != nil && os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		err = os.WriteFile(path, data, 0o644)
	}
	return err
}

func (s *ImageStore) Remove(id ID) { os.Remove(s.path(id)) }

// Open は画像のファイルを開く。手元に無ければ origin から取ってくる。
func (s *ImageStore) Open(id ID, origin uint8) (*os.File, error) {
	f, err := os.Open(s.path(id))
	if err == nil {
		return f, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	if err := s.fetch(id, origin); err != nil {
		return nil, err
	}
	return os.Open(s.path(id))
}

func (s *ImageStore) fetch(id ID, origin uint8) error {
	s.flightMu.Lock()
	if fl, ok := s.flights[id]; ok {
		s.flightMu.Unlock()
		<-fl.done
		return fl.err
	}
	fl := &imageFlight{done: make(chan struct{})}
	s.flights[id] = fl
	s.flightMu.Unlock()
	fl.err = s.fetchFrom(id, origin)
	s.flightMu.Lock()
	delete(s.flights, id)
	s.flightMu.Unlock()
	close(fl.done)
	return fl.err
}

func (s *ImageStore) fetchFrom(id ID, origin uint8) error {
	if origin == s.cfg.Node {
		return fmt.Errorf("image %s is missing on its origin", id.String())
	}
	url := fmt.Sprintf("http://%s:%d/image/%s", s.cfg.Peers[origin], s.cfg.InternalPort, id.String())
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := s.client.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return s.Write(id, data)
	}
	return lastErr
}

// ReadHist は過去の画像の本体を読む。先頭部分しか持たないノードでは持ち主のノードから取ってくる。
func (s *ImageStore) ReadHist(e *HistImage, buf []byte) ([]byte, error) {
	if uint32(cap(buf)) < e.Len {
		buf = make([]byte, e.Len)
	}
	buf = buf[:e.Len]
	ok, err := s.pack.ReadImage(e, buf)
	if err != nil {
		return nil, err
	}
	if ok {
		return buf, nil
	}
	f, err := s.Open(e.ID, 1)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// internalHandler は他のノードからの画像の取得に答える。
func (s *ImageStore) internalHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/image/"
		if len(r.URL.Path) <= len(prefix) {
			http.NotFound(w, r)
			return
		}
		id, ok := ParseID(r.URL.Path[len(prefix):])
		if !ok {
			http.NotFound(w, r)
			return
		}
		if f, err := os.Open(s.path(id)); err == nil {
			defer f.Close()
			if st, err := f.Stat(); err == nil {
				w.Header().Set("Content-Length", fmt.Sprint(st.Size()))
			}
			io.Copy(w, f)
			return
		}
		e := s.pack.FindImage(id)
		if e == nil || e.Len == 0 {
			http.NotFound(w, r)
			return
		}
		buf := make([]byte, e.Len)
		ok, err := s.pack.ReadImage(e, buf)
		if err != nil || !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(buf)
	})
}
