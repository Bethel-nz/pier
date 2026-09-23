package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/health"
	"github.com/Bethel-nz/pier/internal/project"
	"github.com/Bethel-nz/pier/internal/reconcile"
)

func TestStatusTableColumns(t *testing.T) {
	var out bytes.Buffer
	err := Options{Out: &out, Err: &out}.Status(app.StatusResult{
		Project: project.Context{ID: "proj", Root: "/tmp/greppa"},
		Services: []app.ServiceInfo{
			{Name: "web", Host: "localhost", Port: 3000, Path: "/", Public: false, Health: health.Result{Status: health.StatusHealthy}, URL: "https://host.ts.net:8443/", HTTPSPort: 8443},
			{Name: "api", Host: "localhost", Port: 4000, Path: "/api", Public: false, Health: health.Result{Status: health.StatusHealthy}, URL: "https://host.ts.net:8443/api", HTTPSPort: 8443},
			{Name: "webhook", Host: "localhost", Port: 8787, Path: "/hooks", Public: true, Health: health.Result{Status: health.StatusUnavailable}, URL: "https://host.ts.net/hooks", HTTPSPort: 443},
		},
	}, nil)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"SERVICE", "TARGET", "PATH", "PUBLIC", "PAUSED", "HEALTH", "URL",
		"web", "localhost:3000", "/", "false", "healthy", "https://host.ts.net:8443/",
		"api", "localhost:4000", "/api",
		"webhook", "localhost:8787", "/hooks", "true", "unavailable", "https://host.ts.net/hooks",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Status() missing %q in:\n%s", want, got)
		}
	}
}

func TestJSONEnvelopeHasNoColorAndStableFields(t *testing.T) {
	var out bytes.Buffer
	err := Options{JSON: true, Command: "status", Out: &out, Err: &out}.Status(app.StatusResult{
		Project: project.Context{ID: "proj", Root: "/tmp/greppa"},
		DNSName: "host.ts.net",
		Services: []app.ServiceInfo{
			{Name: "web", Host: "localhost", Port: 3000, Path: "/", Health: health.Result{Status: health.StatusHealthy}, URL: "https://host.ts.net:8443/"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	got := out.String()
	if strings.Contains(got, "\x1b") {
		t.Fatalf("JSON output contained color sequences: %q", got)
	}
	for _, want := range []string{`"version": 1`, `"command": "status"`, `"id": "proj"`, `"data"`, `"warnings"`, `"errors"`} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON envelope missing %s in:\n%s", want, got)
		}
	}
}

func TestPlanJSONIncludesOperationKinds(t *testing.T) {
	var out bytes.Buffer
	plan := app.PlanResult{
		Project: project.Context{ID: "proj"},
		Plan: reconcile.Plan{
			Operations: []reconcile.Operation{
				{Kind: reconcile.KindCreate, After: reconcile.Route{Service: "web", HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"}},
				{Kind: reconcile.KindUpdate, Before: reconcile.Route{Path: "/api"}, After: reconcile.Route{Service: "api", HTTPSPort: 8443, Path: "/api", Target: "http://127.0.0.1:4000"}},
				{Kind: reconcile.KindDelete, Before: reconcile.Route{Service: "old", HTTPSPort: 8443, Path: "/old"}},
				{Kind: reconcile.KindKeep, After: reconcile.Route{Service: "hook", HTTPSPort: 443, Path: "/hooks", Public: true}},
			},
		},
	}
	if err := (Options{JSON: true, Command: "plan", Out: &out, Err: &out}).Plan(plan, nil); err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	got := out.String()
	for _, kind := range []string{"create", "update", "delete", "keep"} {
		if !strings.Contains(got, `"kind": "`+kind+`"`) {
			t.Errorf("Plan JSON missing kind %s:\n%s", kind, got)
		}
	}
}
