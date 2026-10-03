package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/health"
	"github.com/Bethel-nz/pier/internal/project"
	"github.com/Bethel-nz/pier/internal/runner"
)

func TestStatusShowsFailingAndHealedRoutes(t *testing.T) {
	now := time.Now()
	result := app.StatusResult{
		Project: project.Context{ID: "proj"},
		Services: []app.ServiceInfo{
			{Name: "webhook", URL: "https://host.ts.net/hooks", Public: true, VerifyError: "answered 502 Bad Gateway", FailingSince: time.Date(2026, 10, 3, 14, 2, 0, 0, time.Local)},
			{Name: "web", URL: "https://host.ts.net:8443/", VerifiedAt: now, RepairedAt: now.Add(-2 * time.Minute)},
			{Name: "api", URL: "https://host.ts.net:8443/api", VerifiedAt: now, RepairedAt: now.Add(-3 * time.Hour)},
		},
	}
	var out bytes.Buffer
	if err := (Options{Out: &out, Err: &out}).Status(result, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"failing      webhook: failing since 14:02 (answered 502 Bad Gateway)",
		"healed       web: its route went missing and Pier put it back 2m ago",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Status() missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "healed       api") {
		t.Errorf("Status() still mentions a repair from hours ago:\n%s", got)
	}

	out.Reset()
	if err := (Options{JSON: true, Out: &out, Err: &out}).Status(result, nil); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"verifyError": "answered 502 Bad Gateway"`, `"failingSince": `, `"verifiedAt": `, `"repairedAt": `} {
		if !strings.Contains(out.String(), field) {
			t.Errorf("status --json missing %s in %s", field, out.String())
		}
	}
}

func TestStatusShowsCrashedCommandsAndUnhealthyServices(t *testing.T) {
	result := app.StatusResult{
		Project: project.Context{ID: "proj"},
		Services: []app.ServiceInfo{
			{Name: "api", Process: &runner.ProcessStatus{Name: "api", State: runner.StateCrashed, Exit: "exited with code 1", Output: []string{"starting", "Error: listen EADDRINUSE :4000"}}},
			{Name: "worker", Process: &runner.ProcessStatus{Name: "worker", State: runner.StateRestarting, Exit: "exited with code 2", Restarts: 1}},
			{Name: "web", Process: &runner.ProcessStatus{Name: "web", State: runner.StateRunning}, Health: health.Result{Status: health.StatusUnhealthy, Code: 500, Error: "GET /healthz returned 500"}},
		},
	}
	var out bytes.Buffer
	if err := (Options{Out: &out, Err: &out}).Status(result, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"crashed      api (exit 1): Error: listen EADDRINUSE :4000\n",
		"restarting   worker (exit 2, restart 2)\n",
		"unhealthy    web: GET /healthz returned 500\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Status() missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "running") {
		t.Errorf("Status() mentions a command that is simply running:\n%s", got)
	}

	out.Reset()
	if err := (Options{Out: &out, Err: &out, JSON: true}).Status(result, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"state": "crashed"`, `"exit": "exited with code 1"`, `"healthError": "GET /healthz returned 500"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("JSON status missing %s in:\n%s", want, out.String())
		}
	}
}
