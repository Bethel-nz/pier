package capture

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTripRedactsAndPrunes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), ".pier", "capture.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now()
	old := Exchange{Time: now.Add(-2 * time.Hour), Service: "api", Host: "myapp.local", Method: "POST", URL: "/hooks/stripe?x=1",
		RequestHeader: http.Header{"Authorization": {"Bearer secret"}, "Stripe-Signature": {"t=1,v1=abc"}},
		RequestBody:   []byte(`{"type":"invoice.paid"}`), Status: 200, ResponseHeader: http.Header{"Set-Cookie": {"s=1"}}, Duration: 12 * time.Millisecond}
	recent := old
	recent.Time = now
	recent.URL = "/hooks/stripe?x=2"
	other := old
	other.Service = "web"
	if err := store.Add(ctx, []Exchange{old, recent, other}); err != nil {
		t.Fatal(err)
	}

	listed, err := store.List(ctx, Query{Service: "api"})
	if err != nil || len(listed) != 2 || listed[0].URL != "/hooks/stripe?x=2" {
		t.Fatalf("newest-first list = %+v, %v", listed, err)
	}
	got, err := store.Get(ctx, listed[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RequestHeader.Get("Authorization") != "[redacted]" || got.ResponseHeader.Get("Set-Cookie") != "[redacted]" {
		t.Fatal("credentials were stored")
	}
	if got.RequestHeader.Get("Stripe-Signature") != "t=1,v1=abc" || string(got.RequestBody) != `{"type":"invoice.paid"}` || got.Duration != 12*time.Millisecond {
		t.Fatalf("exchange did not round-trip: %+v", got)
	}

	since, _ := store.List(ctx, Query{Since: now.Add(-time.Minute), Oldest: true})
	if len(since) != 1 || since[0].URL != "/hooks/stripe?x=2" {
		t.Fatalf("since = %+v", since)
	}

	if n, err := store.Prune(ctx, "api", now.Add(-time.Hour)); err != nil || n != 1 {
		t.Fatalf("prune = %d, %v", n, err)
	}
	if err := store.PruneExcept(ctx, []string{"api"}); err != nil {
		t.Fatal(err)
	}
	all, _ := store.List(ctx, Query{})
	if len(all) != 1 || all[0].Service != "api" {
		t.Fatalf("after pruning = %+v", all)
	}
	if _, err := store.Get(ctx, 999); err == nil {
		t.Fatal("missing id should fail")
	}
}

func TestOpenExistingReportsNoCaptures(t *testing.T) {
	if _, err := OpenExisting(filepath.Join(t.TempDir(), "capture.db")); err != ErrNoCaptures {
		t.Fatalf("err = %v", err)
	}
}

func TestRecorderFlushesOnStop(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "capture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	recorder := NewRecorder(store)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { recorder.Run(ctx); close(done) }()
	for i := 0; i < 5; i++ {
		recorder.Record(Exchange{Time: time.Now(), Service: "api", Method: "GET", URL: "/", RequestHeader: http.Header{}, ResponseHeader: http.Header{}})
	}
	cancel()
	<-done
	all, _ := store.List(context.Background(), Query{})
	if len(all) != 5 {
		t.Fatalf("stored %d of 5", len(all))
	}
}
