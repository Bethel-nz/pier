package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/Bethel-nz/pier/internal/config"
	"github.com/Bethel-nz/pier/internal/localname"
	"github.com/Bethel-nz/pier/internal/state"
)

type fakeLocal struct {
	syncs  [][]string
	roots  []string
	report localname.Report
}

func (f *fakeLocal) Sync(_ context.Context, root string, names []string) (localname.Report, error) {
	f.syncs = append(f.syncs, names)
	f.roots = append(f.roots, root)
	return f.report, nil
}

func (f *fakeLocal) Status() localname.Report { return f.report }

// listingEnv is a store that can also list other projects' saved domains.
type listingEnv struct {
	*fakeEnv
	others []state.ProjectState
}

func (l listingEnv) List() ([]state.ProjectState, error) { return l.others, nil }

func domainEnv() *fakeEnv {
	env := newEnv()
	env.raw.Services = map[string]config.Service{
		"web": {Target: "localhost:3000", Domain: "greppa.local"},
		"api": {Target: "localhost:4000", Path: "/api", Domain: "api.greppa.local"},
	}
	env.after = env.desiredRoutes()
	return env
}

func liveReport(names ...string) localname.Report {
	report := localname.Report{Running: true, HTTPSPort: 443, Names: map[string]localname.NameStatus{}}
	for _, name := range names {
		report.Names[name] = localname.NameStatus{Name: name, State: localname.StateLive}
	}
	return report
}

func TestUpServesDomainsAndReportsLiveURLs(t *testing.T) {
	env := domainEnv()
	local := &fakeLocal{report: liveReport("greppa.local", "api.greppa.local")}
	svc := env.service()
	svc.EnableLocalNames(local)

	result, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Up() = %v", err)
	}
	if len(local.syncs) != 1 || !reflect.DeepEqual(local.syncs[0], []string{"api.greppa.local", "greppa.local"}) {
		t.Fatalf("Sync names = %v", local.syncs)
	}
	if local.roots[0] != env.project.Root {
		t.Fatalf("Sync root = %q", local.roots[0])
	}
	if env.saved == nil || len(env.saved.Domains) != 2 || env.saved.Domains[0].Target != "http://127.0.0.1:4000" {
		t.Fatalf("saved domains = %+v, want targets recorded", env.saved)
	}
	for _, info := range result.Services {
		if info.LocalURL != "https://"+info.Domain+"/" || info.LocalState != localname.StateLive {
			t.Fatalf("%s local = %q (%s)", info.Name, info.LocalURL, info.LocalState)
		}
	}
}

func TestUpShowsNoLocalURLUntilTheNameIsLive(t *testing.T) {
	env := domainEnv()
	report := liveReport("api.greppa.local")
	report.Names["greppa.local"] = localname.NameStatus{Name: "greppa.local", State: localname.StateConflict}
	svc := env.service()
	svc.EnableLocalNames(&fakeLocal{report: report})

	result, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root})
	if err != nil {
		t.Fatal(err)
	}
	web := lookupService(result.Services, "web")
	if web.LocalURL != "" || web.LocalState != localname.StateConflict {
		t.Fatalf("web local = %q (%s), want no URL while conflicting", web.LocalURL, web.LocalState)
	}
}

func TestTakenDomainIsRefusedBeforeTailscaleChanges(t *testing.T) {
	env := domainEnv()
	svc := env.service()
	svc.store = listingEnv{fakeEnv: env, others: []state.ProjectState{{
		ProjectID: "someone-else",
		Domains:   []state.LocalDomain{{Name: "greppa.local", Target: "http://127.0.0.1:9000"}},
	}}}
	local := &fakeLocal{}
	svc.EnableLocalNames(local)

	_, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root})
	if err == nil {
		t.Fatal("Up() succeeded with a name another project owns")
	}
	if env.mutated {
		t.Fatalf("Tailscale was changed before the conflict was refused: %v", env.applyCalls)
	}
	if env.saved != nil || len(local.syncs) != 0 {
		t.Fatal("state saved or daemon synced after refusal")
	}
}

func TestPausedServiceIsWithdrawnLocally(t *testing.T) {
	env := domainEnv()
	local := &fakeLocal{report: liveReport("api.greppa.local")}
	svc := env.service()
	svc.EnableLocalNames(local)

	result, err := svc.Pause(context.Background(), PauseRequest{Start: env.project.Root, Service: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(local.syncs[len(local.syncs)-1], []string{"api.greppa.local"}) {
		t.Fatalf("Sync names = %v, want only the unpaused name", local.syncs)
	}
	if result.Service.LocalState != "paused" || result.Service.LocalURL != "" {
		t.Fatalf("paused web local = %q (%s)", result.Service.LocalURL, result.Service.LocalState)
	}
}

func TestDownWithdrawsEveryName(t *testing.T) {
	env := domainEnv()
	env.state.Domains = []state.LocalDomain{{Service: "web", Name: "greppa.local", Target: "http://127.0.0.1:3000"}}
	env.state.Path = env.project.Root
	local := &fakeLocal{}
	svc := env.service()
	svc.EnableLocalNames(local)

	if _, err := svc.Down(context.Background(), DownRequest{Start: env.project.Root}); err != nil {
		t.Fatal(err)
	}
	if len(local.syncs) != 1 || len(local.syncs[0]) != 0 {
		t.Fatalf("Sync after down = %v, want one call with no names", local.syncs)
	}
	if env.saved == nil || len(env.saved.Domains) != 0 {
		t.Fatalf("saved domains after down = %+v", env.saved)
	}
}

func TestStatusMarksNamesDownWhenTheDaemonIsNotRunning(t *testing.T) {
	env := domainEnv()
	svc := env.service()
	svc.EnableLocalNames(&fakeLocal{report: localname.Report{}})

	result, err := svc.Status(context.Background(), StatusRequest{Start: env.project.Root})
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range result.Services {
		if info.LocalURL != "" || info.LocalState != "down" {
			t.Fatalf("%s local = %q (%s), want down with no URL", info.Name, info.LocalURL, info.LocalState)
		}
	}
}

func TestProjectsWithoutDomainsNeverTouchTheDaemon(t *testing.T) {
	env := newEnv()
	env.after = env.desiredRoutes()
	local := &fakeLocal{}
	svc := env.service()
	svc.EnableLocalNames(local)
	if _, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root}); err != nil {
		t.Fatal(err)
	}
	if len(local.syncs) != 0 {
		t.Fatalf("Sync called %d times for a project with no domains", len(local.syncs))
	}
}

func TestCopyLocalReturnsTheServedURL(t *testing.T) {
	env := domainEnv()
	svc := env.service()
	svc.EnableLocalNames(&fakeLocal{report: liveReport("greppa.local")})

	result, err := svc.Copy(context.Background(), CopyRequest{Start: env.project.Root, Service: "web", Local: true})
	if err != nil || result.URL != "https://greppa.local/" {
		t.Fatalf("Copy(--local) = %q, %v", result.URL, err)
	}
	if _, err := svc.Copy(context.Background(), CopyRequest{Start: env.project.Root, Service: "api", Local: true}); err == nil {
		t.Fatal("Copy(--local) of a name that is not live should explain why")
	}
}
