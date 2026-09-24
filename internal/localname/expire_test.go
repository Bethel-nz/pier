package localname

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bethel-nz/pier/internal/state"
)

func TestCloseWindowsCallsExpireOnceAndRetriesLater(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var calls []string
	failing := errors.New("tailscale is down")
	d := &daemon{ctx: context.Background(), expiring: map[string]*expiry{}, expire: func(_ context.Context, root string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, root)
		return failing
	}}
	saved := []state.ProjectState{{
		ProjectID: "p", Name: "demo", Path: "/work/demo",
		Routes:      []state.Route{{Service: "web", HTTPSPort: 443, Path: "/"}},
		PublicUntil: map[string]time.Time{"web": now.Add(-time.Second)},
	}}

	pending, _ := d.closeWindows(saved, now)
	if !pending {
		t.Fatal("a timed public route keeps the daemon running")
	}
	waitCalls := func(n int) {
		deadline := time.Now().Add(2 * time.Second)
		for {
			mu.Lock()
			got := len(calls)
			mu.Unlock()
			if got == n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("expire called %d times, want %d", got, n)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitCalls(1)
	time.Sleep(20 * time.Millisecond) // let the failed attempt record its error

	_, warnings := d.closeWindows(saved, now.Add(time.Second))
	waitCalls(1) // too soon to retry
	if len(warnings) != 1 || !strings.Contains(warnings[0], "tailscale is down") {
		t.Fatalf("warnings = %v", warnings)
	}
	d.closeWindows(saved, now.Add(expireRetry))
	waitCalls(2)

	saved[0].Routes[0].HTTPSPort = 8443 // closed
	if pending, warnings := d.closeWindows(saved, now.Add(time.Hour)); pending || len(warnings) != 0 {
		t.Fatalf("after closing: pending=%v warnings=%v", pending, warnings)
	}
}
