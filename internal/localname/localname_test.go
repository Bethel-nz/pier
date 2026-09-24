package localname

import (
	"context"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bethel-nz/pier/internal/certs"
	"github.com/Bethel-nz/pier/internal/state"
)

func useConfigDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("AppData", dir)
}

func TestRoutesKeepTheFirstClaimAndReportTheRest(t *testing.T) {
	projects := []state.ProjectState{
		{ProjectID: "b", Path: "/b", Domains: []state.LocalDomain{{Service: "shop", Name: "app.local", Target: "http://127.0.0.1:5000"}}},
		{ProjectID: "a", Path: "/a", Domains: []state.LocalDomain{
			{Service: "web", Name: "app.local", Target: "http://127.0.0.1:3000"},
			{Service: "api", Name: "api.local", Target: "http://127.0.0.1:4000"},
		}},
		{ProjectID: "c", Path: "/c", Domains: []state.LocalDomain{{Service: "old", Name: "old.local"}}}, // saved before targets existed
	}
	routes, conflicts := Routes(projects)
	if len(routes) != 2 || routes[0].Name != "api.local" || routes[1].Name != "app.local" {
		t.Fatalf("routes = %+v", routes)
	}
	if routes[1].ProjectID != "a" || routes[1].CertDir != filepath.Join("/a", ".pier", "certs") {
		t.Fatalf("app.local route = %+v, want project a's claim", routes[1])
	}
	if len(conflicts) != 1 || conflicts[0].ProjectID != "b" || conflicts[0].Winner != "a" {
		t.Fatalf("conflicts = %+v", conflicts)
	}
}

func TestHeartbeatURLAndFreshness(t *testing.T) {
	now := time.Now()
	beat := Heartbeat{PID: 1, UpdatedAt: now, HTTPSPort: 443}
	if got := beat.URL("app.local"); got != "https://app.local/" {
		t.Fatalf("URL = %q", got)
	}
	beat.HTTPSPort = 8443
	if got := beat.URL("app.local"); got != "https://app.local:8443/" {
		t.Fatalf("fallback URL = %q", got)
	}
	if !beat.Fresh(now.Add(time.Second)) || beat.Fresh(now.Add(staleAfter+time.Second)) {
		t.Fatal("freshness window wrong")
	}
}

func TestReportOnlyGivesURLsForLiveNames(t *testing.T) {
	report := Report{Running: true, HTTPSPort: 443, Names: map[string]NameStatus{
		"app.local":  {Name: "app.local", State: StateLive},
		"shop.local": {Name: "shop.local", State: StateConflict},
	}}
	if report.URL("app.local") != "https://app.local/" || report.URL("shop.local") != "" || report.URL("gone.local") != "" {
		t.Fatal("URL must be set only for live names")
	}
	if report.State("gone.local") != "down" || (Report{}).State("app.local") != "down" {
		t.Fatal("unknown names and a stopped daemon are down")
	}
}

func TestSettledWaitsForProbing(t *testing.T) {
	beat := Heartbeat{Names: []NameStatus{{Name: "a.local", State: StateLive}, {Name: "b.local", State: StateProbing}}}
	if settled(beat, []string{"a.local", "b.local"}) {
		t.Fatal("settled while b.local is probing")
	}
	beat.Names[1].State = StateConflict
	if !settled(beat, []string{"a.local", "b.local"}) {
		t.Fatal("a conflict is a final answer")
	}
	if settled(beat, []string{"missing.local"}) {
		t.Fatal("settled before the daemon saw the name")
	}
}

func TestHeartbeatAndStopRequestRoundTrip(t *testing.T) {
	useConfigDir(t)
	beat := Heartbeat{PID: 4242, Build: "x", UpdatedAt: time.Now(), HTTPSPort: 443}
	if err := writeHeartbeat(beat); err != nil {
		t.Fatal(err)
	}
	read, err := readHeartbeat()
	if err != nil || read.PID != 4242 {
		t.Fatalf("readHeartbeat = %+v, %v", read, err)
	}
	removeHeartbeat(9999) // another pid must not delete it
	if _, err := readHeartbeat(); err != nil {
		t.Fatal("heartbeat removed by the wrong pid")
	}

	if stopRequested(4242) {
		t.Fatal("stop reported before it was requested")
	}
	if err := requestStop(4242); err != nil {
		t.Fatal(err)
	}
	if stopRequested(1) {
		t.Fatal("stop addressed to another pid")
	}
	if !stopRequested(4242) || stopRequested(4242) {
		t.Fatal("stop request should be seen exactly once")
	}
}

func TestIgnorePierDirAddsGitignoreEntryInRepos(t *testing.T) {
	root := t.TempDir()
	if err := ignorePierDir(root); err != nil {
		t.Fatal(err)
	}
	if _, err := readFileString(filepath.Join(root, ".gitignore")); err == nil {
		t.Fatal("wrote .gitignore outside a git repository")
	}
	if err := mkdir(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileString(filepath.Join(root, ".gitignore"), "node_modules"); err != nil {
		t.Fatal(err)
	}
	if err := ignorePierDir(root); err != nil {
		t.Fatal(err)
	}
	got, _ := readFileString(filepath.Join(root, ".gitignore"))
	if got != "node_modules\n.pier/\n" {
		t.Fatalf(".gitignore = %q", got)
	}
}

func readFileString(path string) (string, error) {
	contents, err := os.ReadFile(path)
	return string(contents), err
}

func writeFileString(path, contents string) error { return os.WriteFile(path, []byte(contents), 0o644) }

func mkdir(path string) error { return os.MkdirAll(path, 0o755) }

func TestCleanRemovesTheLocalFootprintAndKeepsGoing(t *testing.T) {
	useConfigDir(t)
	store := state.New(filepath.Join(t.TempDir(), "projects"))
	d := NewDirectory(store)
	d.caDir = filepath.Join(t.TempDir(), "ca")
	var untrusted string
	d.untrust = func(_ *x509.Certificate, path string) error { untrusted = path; return nil }

	ca, _, err := certs.LoadOrCreateCA(d.caDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if _, err := ca.EnsureLeaf(certs.LeafDir(root), []string{"app.local"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(state.ProjectState{ProjectID: "p1", Name: "demo", Path: root,
		Domains: []state.LocalDomain{{Service: "web", Name: "app.local", Target: "http://127.0.0.1:3000"}}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pierd.log", "locald.pid"} {
		path, _ := runtimePath(name)
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		_ = os.WriteFile(path, []byte("x"), 0o600)
	}

	report := d.Clean(context.Background())
	if len(report.Errors) != 0 {
		t.Fatalf("errors = %v", report.Errors)
	}
	if untrusted != filepath.Join(d.caDir, "ca.pem") || report.RemovedCA != d.caDir || exists(d.caDir) {
		t.Fatalf("CA not untrusted and removed: %+v", report)
	}
	if exists(certs.LeafDir(root)) || len(report.RemovedCerts) != 1 {
		t.Fatalf("project certificate not removed: %+v", report)
	}
	saved, _ := store.Load("p1")
	if len(saved.Domains) != 0 || len(report.ClearedNames) != 1 {
		t.Fatalf("local names not forgotten: %+v", saved.Domains)
	}
	for _, name := range []string{"pierd.log", "locald.pid"} {
		if path, _ := runtimePath(name); exists(path) {
			t.Fatalf("%s survived clean", name)
		}
	}
}

func TestCleanKeepsTheCAWhenTheTrustStoreRefuses(t *testing.T) {
	useConfigDir(t)
	d := NewDirectory(state.New(filepath.Join(t.TempDir(), "projects")))
	d.caDir = filepath.Join(t.TempDir(), "ca")
	d.untrust = func(*x509.Certificate, string) error { return errors.New("sudo: a password is required") }
	if _, _, err := certs.LoadOrCreateCA(d.caDir, time.Now()); err != nil {
		t.Fatal(err)
	}
	report := d.Clean(context.Background())
	if len(report.Errors) != 1 || !exists(filepath.Join(d.caDir, "ca.pem")) {
		t.Fatalf("report = %+v; the CA must stay so a retry removes the same certificate", report)
	}
}
