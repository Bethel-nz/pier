package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/project"
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
