package capture

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPrunesRunsOnItsOwnAndFollowsKeep(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "capture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	exchange := func(service string, age time.Duration) Exchange {
		return Exchange{Time: now.Add(-age), Service: service, Method: "GET", URL: "/", RequestHeader: http.Header{}, ResponseHeader: http.Header{}}
	}
	if err := store.Add(context.Background(), []Exchange{
		exchange("api", 3*time.Minute), // older than 2min: goes
		exchange("api", 30*time.Second),
		exchange("old", time.Second), // no longer captured: goes
	}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	keep := map[string]time.Duration{"api": 2 * time.Minute}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		store.Prunes(ctx, 20*time.Millisecond, func() map[string]time.Duration {
			mu.Lock()
			defer mu.Unlock()
			return keep
		})
		close(done)
	}()

	waitCount := func(want int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			all, _ := store.List(context.Background(), Query{})
			if len(all) == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d requests left, want %d", len(all), want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitCount(1) // pruned right away, without anyone calling it

	mu.Lock()
	keep = map[string]time.Duration{"api": 10 * time.Second} // capture: shortened
	mu.Unlock()
	waitCount(0) // the next tick applies the new window

	cancel()
	<-done
}
