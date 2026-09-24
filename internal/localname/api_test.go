package localname

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bethel-nz/pier/internal/localproxy"
	"github.com/Bethel-nz/pier/internal/state"
)

type fakeActor struct{ args []string }

func (f *fakeActor) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.args = args
	return []byte(`{"command":"pause"}`), nil
}

// testAPI serves the API on 127.0.0.1 exactly as the daemon does, with a
// fake in place of running the pier binary.
func testAPI(t *testing.T) (*fakeActor, string) {
	t.Helper()
	store := state.New(filepath.Join(t.TempDir(), "projects"))
	if err := store.Save(state.ProjectState{
		ProjectID: "p1", Name: "demo", Path: "/work/demo",
		Domains: []state.LocalDomain{{Service: "web", Name: "myapp.local", Target: "http://127.0.0.1:3000"}},
		Paused:  map[string]bool{"api": true},
	}); err != nil {
		t.Fatal(err)
	}
	d := &daemon{projects: store, proxy: localproxy.New()}
	d.beat = Heartbeat{PID: 42, HTTPSPort: 443, Names: []NameStatus{{Name: "myapp.local", State: StateLive}}}
	d.publish()
	actor := &fakeActor{}
	a := &api{d: d, pier: "pier", actor: actor.run}
	server := httptest.NewServer(a.handler())
	t.Cleanup(server.Close)
	a.port = server.Listener.Addr().(*net.TCPAddr).Port
	return actor, "http://127.0.0.1:" + strconv.Itoa(a.port)
}

func TestAPIStatusListsProjectsAndLiveURLs(t *testing.T) {
	_, base := testAPI(t)
	resp, err := http.Get(base + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var status Status
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Daemon.PID != 42 || len(status.Projects) != 1 {
		t.Fatalf("status = %+v", status)
	}
	project := status.Projects[0]
	if project.Name != "demo" || len(project.Names) != 1 || project.Names[0].URL != "https://myapp.local/" || !reflect.DeepEqual(project.Paused, []string{"api"}) {
		t.Fatalf("project = %+v", project)
	}
}

func TestAPIRefusesForeignHostsAndBareWrites(t *testing.T) {
	_, base := testAPI(t)
	// A DNS-rebinding page reaches 127.0.0.1 with its own name in Host.
	req, _ := http.NewRequest(http.MethodGet, base+"/api/status", nil)
	req.Host = "evil.example:" + base[strings.LastIndex(base, ":")+1:]
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign Host: %v %v, want 403", resp.StatusCode, err)
	}
	// A cross-site form or fetch cannot add X-Pier without a preflight.
	resp, err := http.Post(base+"/api/projects/p1/services/web/pause", "text/plain", nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without X-Pier: %v %v, want 403", resp.StatusCode, err)
	}
}

func TestAPIActionsRunTheRealCommand(t *testing.T) {
	actor, base := testAPI(t)
	post := func(path string) int {
		req, _ := http.NewRequest(http.MethodPost, base+path, nil)
		req.Header.Set("X-Pier", "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("/api/projects/p1/services/web/pause"); code != http.StatusOK {
		t.Fatalf("pause = %d", code)
	}
	if want := []string{"--json", "--config", "/work/demo", "pause", "web"}; !reflect.DeepEqual(actor.args, want) {
		t.Fatalf("ran %v, want %v", actor.args, want)
	}
	for path, want := range map[string]int{
		"/api/projects/p1/services/web/delete":     http.StatusNotFound,
		"/api/projects/nope/services/web/pause":    http.StatusNotFound,
		"/api/projects/p1/services/--force/resume": http.StatusBadRequest,
	} {
		if code := post(path); code != want {
			t.Errorf("%s = %d, want %d", path, code, want)
		}
	}
}

func TestAPIEventsStartWithStatus(t *testing.T) {
	_, base := testAPI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type = %q", resp.Header.Get("Content-Type"))
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "event: status\n" {
		t.Fatalf("first event = %q, %v", line, err)
	}
}
