package isureal

import (
	"bufio"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// accessLogger は計測のためのアクセスログ（nginx で出していたものと同じ形）を書く。本番の走行では切る。
type accessLogger struct {
	next http.Handler
	mu   sync.Mutex
	w    *bufio.Writer
}

func newAccessLogger(next http.Handler, path string) *accessLogger {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("failed to open access log: %v", err)
	}
	l := &accessLogger{next: next, w: bufio.NewWriterSize(f, 1<<20)}
	go func() {
		for range time.Tick(time.Second) {
			l.mu.Lock()
			l.w.Flush()
			l.mu.Unlock()
		}
	}()
	return l
}

type loggedWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *loggedWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *loggedWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *loggedWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = 200
	}
	n, err := io.Copy(w.ResponseWriter, r)
	w.bytes += n
	return n, err
}

func (w *loggedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (l *accessLogger) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	lw := &loggedWriter{ResponseWriter: w}
	reqLen := r.ContentLength
	if reqLen < 0 {
		reqLen = 0
	}
	l.next.ServeHTTP(lw, r)
	end := time.Now()
	b := make([]byte, 0, 256)
	b = append(b, "time:"...)
	b = end.UTC().AppendFormat(b, time.RFC3339)
	b = append(b, "\tmsec:"...)
	b = strconv.AppendFloat(b, float64(end.UnixMicro())/1e6, 'f', 3, 64)
	b = append(b, "\tmethod:"...)
	b = append(b, r.Method...)
	b = append(b, "\turi:"...)
	b = append(b, r.RequestURI...)
	b = append(b, "\tstatus:"...)
	b = strconv.AppendInt(b, int64(lw.status), 10)
	b = append(b, "\tbytes:"...)
	b = strconv.AppendInt(b, lw.bytes, 10)
	b = append(b, "\treqbytes:"...)
	b = strconv.AppendInt(b, reqLen+200, 10)
	b = append(b, "\treqtime:"...)
	b = strconv.AppendFloat(b, end.Sub(start).Seconds(), 'f', 3, 64)
	b = append(b, "\tua:"...)
	b = append(b, r.UserAgent()...)
	b = append(b, "\tcc:"...)
	b = append(b, r.Header.Get("X-Country-Code")...)
	b = append(b, "\tip:"...)
	b = append(b, r.Header.Get("X-Client-IP")...)
	b = append(b, '\n')
	l.mu.Lock()
	l.w.Write(b)
	l.mu.Unlock()
}
