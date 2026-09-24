package localproxy

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/Bethel-nz/pier/internal/capture"
)

// captureTo hands every finished request to recorder, bodies capped at
// capture.MaxBody. WebSockets pass through unrecorded: they are streams, not
// requests Pier could send again.
func captureTo(recorder Recorder, service string, next http.Handler) http.Handler {
	if recorder == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		request := &capped{}
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = teeBody{ReadCloser: r.Body, copy: request}
		}
		header := r.Header.Clone()
		response := &captureWriter{ResponseWriter: w}
		next.ServeHTTP(response, r)

		client, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			client = r.RemoteAddr
		}
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		recorder.Record(capture.Exchange{
			Time: start, Service: service, Host: normalizeHost(r.Host), Method: r.Method, URL: r.URL.RequestURI(),
			Client: client, Duration: time.Since(start),
			RequestHeader: header, RequestBody: request.buf.Bytes(), RequestTruncated: request.truncated,
			Status: status, ResponseHeader: response.header, ResponseBody: response.body.buf.Bytes(), ResponseTruncated: response.body.truncated,
		})
	})
}

// capped keeps the first capture.MaxBody bytes written to it.
type capped struct {
	buf       bytes.Buffer
	truncated bool
}

func (c *capped) Write(b []byte) {
	room := capture.MaxBody - c.buf.Len()
	if len(b) > room {
		b = b[:max(room, 0)]
		c.truncated = true
	}
	c.buf.Write(b)
}

type teeBody struct {
	io.ReadCloser
	copy *capped
}

func (t teeBody) Read(p []byte) (int, error) {
	n, err := t.ReadCloser.Read(p)
	t.copy.Write(p[:n])
	return n, err
}

type captureWriter struct {
	http.ResponseWriter
	status int
	header http.Header
	body   capped
}

func (w *captureWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.header = w.ResponseWriter.Header().Clone()
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *captureWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.body.Write(b[:n])
	return n, err
}

func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
