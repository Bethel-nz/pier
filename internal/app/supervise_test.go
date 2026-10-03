package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bethel-nz/pier/internal/localname"
	"github.com/Bethel-nz/pier/internal/localproxy"
	"github.com/Bethel-nz/pier/internal/reconcile"
)

// supervisedEnv is a project that owns all three routes, with web's route
// removed behind Pier's back and an unrelated route Pier does not own.
func supervisedEnv() (*fakeEnv, reconcile.Route) {
	env := newEnv()
	desired := env.desiredRoutes()
	env.state.Routes = ownAll(desired)
	unrelated := reconcile.Route{HTTPSPort: 10000, Path: "/other", Target: "http://127.0.0.1:9999", Public: true}
	var web reconcile.Route
	env.actual = []reconcile.Route{unrelated}
	for _, route := range desired {
		if route.Service == "web" {
			web = route
			continue
		}
		env.actual = append(env.actual, route)
	}
	env.after = append(append([]reconcile.Route(nil), desired...), unrelated)
	return env, web
}

func okFetch(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func TestSuperviseRestoresOnlyMissingOwnedRoutes(t *testing.T) {
	env, web := supervisedEnv()
	svc := env.service()
	svc.fetch = okFetch

	pass, err := svc.Supervise(context.Background(), env.project.Root)
	if err != nil {
		t.Fatalf("Supervise() = %v", err)
	}
	if len(env.applyCalls) != 1 || env.applyCalls[0].Kind != reconcile.KindCreate || env.applyCalls[0].After.Key() != web.Key() {
		t.Fatalf("mutations = %#v, want only web put back", env.applyCalls)
	}
	if len(pass.Repaired) != 1 || pass.Repaired[0] != "web" {
		t.Fatalf("repaired = %v, want [web]", pass.Repaired)
	}
	if env.saved != nil {
		t.Fatalf("Supervise saved state %#v, want it left alone", env.saved)
	}
	if len(pass.Probes) != 3 {
		t.Fatalf("probes = %#v, want one per owned route", pass.Probes)
	}
}

func TestSuperviseLeavesHealthyRoutesAlone(t *testing.T) {
	env := newEnv()
	env.actual = env.desiredRoutes()
	env.state.Routes = ownAll(env.actual)
	svc := env.service()
	svc.fetch = okFetch

	pass, err := svc.Supervise(context.Background(), env.project.Root)
	if err != nil || env.mutated || len(pass.Repaired) != 0 {
		t.Fatalf("Supervise() = %v, mutated=%v repaired=%v; want nothing to do", err, env.mutated, pass.Repaired)
	}
}

func TestSuperviseDoesNotApplyAnEditedConfig(t *testing.T) {
	env, _ := supervisedEnv()
	dir := t.TempDir()
	env.project.Root, env.project.ConfigPath = dir, filepath.Join(dir, "pier.yaml")
	if err := os.WriteFile(env.project.ConfigPath, []byte("edited since pier up"), 0o644); err != nil {
		t.Fatal(err)
	}
	env.state.ConfigHash = "sha256:from-the-last-pier-up"
	svc := env.service()
	svc.fetch = okFetch

	pass, err := svc.Supervise(context.Background(), env.project.Root)
	if err != nil || env.mutated {
		t.Fatalf("Supervise() = %v, mutated=%v; want no repair", err, env.mutated)
	}
	if !strings.Contains(pass.Note, "pier.yaml changed") || len(pass.Probes) == 0 {
		t.Fatalf("pass = %+v, want a note and probes still run", pass)
	}
}

func TestSuperviseReportsTailscaleOffline(t *testing.T) {
	env, _ := supervisedEnv()
	env.checkErr = errors.New("tailscale is not running")
	svc := env.service()
	svc.fetch = okFetch

	_, err := svc.Supervise(context.Background(), env.project.Root)
	var prereq *PrerequisiteError
	if !errors.As(err, &prereq) || env.mutated {
		t.Fatalf("Supervise() = %v mutated=%v, want a prerequisite error and no change", err, env.mutated)
	}
}

func TestProbeCountsOnlyMissingAnswersAnd5xxAsFailing(t *testing.T) {
	env := newEnv()
	svc := env.service()
	cases := []struct {
		status  int
		err     error
		failing bool
	}{
		{http.StatusOK, nil, false},
		{http.StatusNotFound, nil, false}, // the app answered; the route works
		{http.StatusBadGateway, nil, true},
		{0, errors.New("dial tcp: connection refused"), true},
	}
	for _, tc := range cases {
		svc.fetch = func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodHead || req.Header.Get(localproxy.ProbeHeader) == "" {
				t.Fatalf("probe sent %s without %s", req.Method, localproxy.ProbeHeader)
			}
			if tc.err != nil {
				return nil, tc.err
			}
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		probe := svc.probe(context.Background(), "web", "https://host.ts.net:8443/")
		if (probe.Error != "") != tc.failing {
			t.Errorf("status %d err %v: probe = %+v, failing want %v", tc.status, tc.err, probe, tc.failing)
		}
	}
}

func TestStatusShowsBackgroundChecks(t *testing.T) {
	env := newEnv()
	env.actual = env.desiredRoutes()
	env.state.Routes = ownAll(env.actual)
	since := env.now.Add(-10 * time.Minute)
	report := localname.Report{Running: true, Supervision: map[string]localname.Supervision{env.project.ID: {
		Project:  env.project.ID,
		Repaired: map[string]time.Time{"web": env.now.Add(-2 * time.Minute)},
		Probes: []localname.Probe{
			{Service: "webhook", URL: "https://host.ts.net/hooks", Error: "answered 502 Bad Gateway", FailingSince: since},
			{Service: "api", URL: "https://host.ts.net:8443/api", Status: 200, LastSuccess: env.now},
		},
	}}}
	svc := env.service()
	svc.EnableLocalNames(&fakeLocal{report: report})

	result, err := svc.Status(context.Background(), StatusRequest{Start: env.project.Root})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ServiceInfo{}
	for _, info := range result.Services {
		byName[info.Name] = info
	}
	if hook := byName["webhook"]; hook.VerifyError == "" || !hook.FailingSince.Equal(since) {
		t.Errorf("webhook = %+v, want failing since %v", hook, since)
	}
	if api := byName["api"]; !api.VerifiedAt.Equal(env.now) || api.VerifyError != "" {
		t.Errorf("api = %+v, want verified", api)
	}
	if web := byName["web"]; web.RepairedAt.IsZero() {
		t.Errorf("web = %+v, want its repair time", web)
	}
}
