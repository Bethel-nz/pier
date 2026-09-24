package localproxy

import (
	"context"
	"io"
	"net/http"
	"time"
)

// Shaping slows a service down to a slower network. The zero value is full speed.
type Shaping struct {
	Latency time.Duration
	Down    int64 // bytes per second toward the client; 0 is unlimited
	Up      int64 // bytes per second toward the service; 0 is unlimited
}

// shape adds latency once per request and paces both bodies.
func shape(s Shaping, next http.Handler) http.Handler {
	if s == (Shaping{}) {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Latency > 0 {
			select {
			case <-time.After(s.Latency):
			case <-r.Context().Done():
				return
			}
		}
		if s.Up > 0 && r.Body != nil && r.Body != http.NoBody {
			r.Body = &slowBody{ReadCloser: r.Body, pace: newPace(r.Context(), s.Up)}
		}
		if s.Down > 0 {
			w = &slowWriter{ResponseWriter: w, pace: newPace(r.Context(), s.Down)}
		}
		next.ServeHTTP(w, r)
	})
}

// pace holds a stream to rate bytes per second, measured from its start.
type pace struct {
	ctx   context.Context
	rate  int64
	start time.Time
	done  int64
}

func newPace(ctx context.Context, rate int64) *pace {
	return &pace{ctx: ctx, rate: rate, start: time.Now()}
}

// chunk is how much moves between pauses: a tenth of a second's worth.
func (p *pace) chunk() int { return int(max(p.rate/10, 512)) }

// account records n bytes and sleeps until the stream is back on schedule.
func (p *pace) account(n int) error {
	p.done += int64(n)
	due := p.start.Add(time.Duration(float64(p.done) / float64(p.rate) * float64(time.Second)))
	if wait := time.Until(due); wait > 0 {
		select {
		case <-time.After(wait):
		case <-p.ctx.Done():
			return p.ctx.Err()
		}
	}
	return nil
}

type slowWriter struct {
	http.ResponseWriter
	pace *pace
}

func (w *slowWriter) Write(b []byte) (int, error) {
	written := 0
	for len(b) > 0 {
		n := min(len(b), w.pace.chunk())
		m, err := w.ResponseWriter.Write(b[:n])
		written += m
		if err != nil {
			return written, err
		}
		if err := w.pace.account(m); err != nil {
			return written, err
		}
		if f, ok := w.ResponseWriter.(http.Flusher); ok {
			f.Flush() // the client should see the trickle, not one burst at the end
		}
		b = b[n:]
	}
	return written, nil
}

// Unwrap keeps WebSocket hijacking and streaming flushes working.
func (w *slowWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type slowBody struct {
	io.ReadCloser
	pace *pace
}

func (b *slowBody) Read(p []byte) (int, error) {
	if len(p) > b.pace.chunk() {
		p = p[:b.pace.chunk()]
	}
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		if waitErr := b.pace.account(n); waitErr != nil {
			return n, waitErr
		}
	}
	return n, err
}
