package localproxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTrafficRecordsRequestsNewestFirst(t *testing.T) {
	p := newProxy(t, upstream(t).URL)
	live, cancel := p.Subscribe()
	defer cancel()

	for _, path := range []string{"/a", "/b", "/c"} {
		p.HTTPS().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "https://my-app.local"+path, nil))
	}
	p.HTTPS().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "https://other.local/x", nil))

	recent := p.Recent("My-App.local", 2)
	if len(recent) != 2 || recent[0].Path != "/c" || recent[1].Path != "/b" {
		t.Fatalf("recent = %+v, want /c then /b", recent)
	}
	if recent[0].Status != http.StatusOK || recent[0].Bytes == 0 || recent[0].Method != http.MethodGet {
		t.Fatalf("request = %+v, want status, size, and method", recent[0])
	}
	if all := p.Recent("", 10); len(all) != 4 || all[0].Status != http.StatusNotFound {
		t.Fatalf("all = %+v, want 4 with the unknown host's 404 first", all)
	}
	select {
	case r := <-live:
		if r.Path != "/a" {
			t.Fatalf("first live event = %s, want /a", r.Path)
		}
	case <-time.After(time.Second):
		t.Fatal("no live event")
	}
}

func TestTrafficRingKeepsTheLatest(t *testing.T) {
	tr := newTraffic()
	for i := 0; i < trafficCapacity+10; i++ {
		tr.add(Request{Host: "a.local", Status: i})
	}
	recent := tr.recent("", trafficCapacity+10)
	if len(recent) != trafficCapacity || recent[0].Status != trafficCapacity+9 || recent[len(recent)-1].Status != 10 {
		t.Fatalf("ring holds %d, newest %d, oldest %d", len(recent), recent[0].Status, recent[len(recent)-1].Status)
	}
}
