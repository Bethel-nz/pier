package localproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bethel-nz/pier/internal/capture"
)

type memoryRecorder struct {
	mu        sync.Mutex
	exchanges []capture.Exchange
}

func (m *memoryRecorder) Record(e capture.Exchange) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exchanges = append(m.exchanges, e)
}

func TestTapCapturesAndKeepsForwardedHeaders(t *testing.T) {
	var seen http.Header
	var seenHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, seenHost = r.Header.Clone(), r.Host
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Reply", "yes")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("got " + string(body)))
	}))
	defer upstream.Close()

	recorder := &memoryRecorder{}
	tap, err := New().Tap(Route{Target: upstream.URL, Service: "hooks", Capture: recorder})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://host.ts.net/hooks/stripe?id=1", strings.NewReader(`{"paid":true}`))
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Stripe-Signature", "v1=abc")
	rec := httptest.NewRecorder()
	tap.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted || rec.Body.String() != `got {"paid":true}` {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	if seen.Get("X-Forwarded-For") != "203.0.113.9" || seen.Get("X-Forwarded-Proto") != "https" || seenHost != strings.TrimPrefix(upstream.URL, "http://") {
		t.Fatalf("upstream saw Host %q and %v", seenHost, seen)
	}
	if len(recorder.exchanges) != 1 {
		t.Fatalf("captured %d exchanges", len(recorder.exchanges))
	}
	e := recorder.exchanges[0]
	if e.Service != "hooks" || e.Method != "POST" || e.URL != "/hooks/stripe?id=1" || string(e.RequestBody) != `{"paid":true}` ||
		e.RequestHeader.Get("Stripe-Signature") != "v1=abc" || e.Status != http.StatusAccepted ||
		string(e.ResponseBody) != `got {"paid":true}` || e.ResponseHeader.Get("X-Reply") != "yes" {
		t.Fatalf("captured %+v", e)
	}
}

func TestThrottleAddsLatencyAndPacesBodies(t *testing.T) {
	payload := strings.Repeat("x", 20_000)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer upstream.Close()

	p := New()
	if err := p.SetRoutes([]Route{{Host: "slow.local", Target: upstream.URL, Service: "web",
		Shaping: Shaping{Latency: 150 * time.Millisecond, Down: 50_000}}}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	rec := httptest.NewRecorder()
	p.HTTPS().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://slow.local/", nil))
	elapsed := time.Since(start)
	if rec.Body.Len() != len(payload) {
		t.Fatalf("body = %d bytes", rec.Body.Len())
	}
	// 150ms latency + 20kB at 50kB/s = 400ms of pacing.
	if elapsed < 500*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("took %v, want about 550ms", elapsed)
	}
}

func TestUnchangedRouteKeepsItsProxy(t *testing.T) {
	p := New()
	route := Route{Host: "a.local", Target: "http://127.0.0.1:1", Service: "a"}
	if err := p.SetRoutes([]Route{route}); err != nil {
		t.Fatal(err)
	}
	first := p.routes["a.local"]
	_ = p.SetRoutes([]Route{route})
	if p.routes["a.local"] != first {
		t.Fatal("an unchanged route was rebuilt, dropping its idle connections")
	}
	route.Shaping = Shaping{Latency: time.Millisecond}
	_ = p.SetRoutes([]Route{route})
	if p.routes["a.local"] == first {
		t.Fatal("a changed throttle did not rebuild the route")
	}
}
