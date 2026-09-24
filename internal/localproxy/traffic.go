package localproxy

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// trafficCapacity is how many recent requests the proxy remembers in memory.
const trafficCapacity = 500

// Request is one proxied request, as the dashboard shows it.
type Request struct {
	Time     time.Time `json:"time"`
	Host     string    `json:"host"`
	Method   string    `json:"method"`
	Path     string    `json:"path"`
	Status   int       `json:"status"`
	Duration int64     `json:"durationMs"`
	Bytes    int64     `json:"bytes"`
	Client   string    `json:"client"`
	Agent    string    `json:"userAgent,omitempty"`
}

// traffic is a ring buffer of recent requests plus live subscribers.
type traffic struct {
	mu          sync.Mutex
	ring        []Request
	next        int
	full        bool
	subscribers map[chan Request]struct{}
}

func newTraffic() *traffic {
	return &traffic{ring: make([]Request, trafficCapacity), subscribers: map[chan Request]struct{}{}}
}

func (t *traffic) add(r Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ring[t.next] = r
	t.next = (t.next + 1) % len(t.ring)
	if t.next == 0 {
		t.full = true
	}
	for ch := range t.subscribers {
		select {
		case ch <- r:
		default: // a slow reader misses live events; it can re-read Recent
		}
	}
}

// recent returns up to limit requests for host (all hosts when empty), newest first.
func (t *traffic) recent(host string, limit int) []Request {
	t.mu.Lock()
	defer t.mu.Unlock()
	count := t.next
	if t.full {
		count = len(t.ring)
	}
	out := make([]Request, 0, min(limit, count))
	for i := 1; i <= count && len(out) < limit; i++ {
		r := t.ring[(t.next-i+len(t.ring))%len(t.ring)]
		if host == "" || r.Host == host {
			out = append(out, r)
		}
	}
	return out
}

func (t *traffic) subscribe() (chan Request, func()) {
	ch := make(chan Request, 64)
	t.mu.Lock()
	t.subscribers[ch] = struct{}{}
	t.mu.Unlock()
	return ch, func() {
		t.mu.Lock()
		delete(t.subscribers, ch)
		t.mu.Unlock()
	}
}

// Recent returns up to limit recent requests for host, newest first.
func (p *Proxy) Recent(host string, limit int) []Request {
	return p.traffic.recent(normalizeHost(host), limit)
}

// Subscribe streams requests as they finish until cancel is called.
func (p *Proxy) Subscribe() (<-chan Request, func()) {
	return p.traffic.subscribe()
}

// recorder captures status and size while staying transparent to the reverse
// proxy: Unwrap lets it hijack WebSockets and flush SSE through the original.
type recorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// record wraps next so every request lands in the traffic buffer.
func (p *Proxy) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 && r.Header.Get("Upgrade") != "" {
			status = http.StatusSwitchingProtocols // hijacked WebSocket
		}
		client, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			client = r.RemoteAddr
		}
		p.traffic.add(Request{
			Time: start, Host: normalizeHost(r.Host), Method: r.Method, Path: r.URL.Path,
			Status: status, Duration: time.Since(start).Milliseconds(), Bytes: rec.bytes,
			Client: client, Agent: r.UserAgent(),
		})
	})
}
