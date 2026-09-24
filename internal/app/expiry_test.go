package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Bethel-nz/pier/internal/config"
	"github.com/Bethel-nz/pier/internal/reconcile"
	"github.com/Bethel-nz/pier/internal/state"
)

func timedProject(t *testing.T) config.Project {
	t.Helper()
	cfg, err := config.Normalize(config.Config{Version: 1, Name: "p", Services: map[string]config.Service{
		"demo":  {Target: "localhost:3000", Public: &config.Public{On: true, For: 2 * time.Hour}},
		"hooks": {Target: "localhost:8787", Path: "/hooks", Public: config.PublicFlag(true)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func public(cfg config.Project) map[string]bool {
	out := map[string]bool{}
	for _, service := range cfg.Services {
		out[service.Name] = service.Public
	}
	return out
}

func TestPublicWindowOpensOnUpAndCloses(t *testing.T) {
	cfg := timedProject(t)
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

	until := renewWindows(cfg, now)
	if len(until) != 1 || !until["demo"].Equal(now.Add(2*time.Hour)) {
		t.Fatalf("windows = %v", until)
	}
	if open := public(withExpiry(cfg, until, now.Add(time.Hour))); !open["demo"] || !open["hooks"] {
		t.Fatalf("inside the window: %v", open)
	}
	closed := withExpiry(cfg, until, now.Add(2*time.Hour))
	if got := public(closed); got["demo"] || !got["hooks"] {
		t.Fatalf("after the window: %v (an untimed public service must stay public)", got)
	}
	for _, service := range closed.Services {
		if service.Name == "demo" && service.HTTPSPort != 8443 {
			t.Fatal("an expired service must move to the private listener")
		}
	}
	if public(withExpiry(cfg, nil, now))["demo"] {
		t.Fatal("a timed service with no window yet must not be public")
	}

	// pier share on an expired service wins, and shows no countdown.
	services := effectiveServices(closed, map[string]bool{"demo": true})
	infos := serviceInfos(services, "host.ts.net", nil, nil)
	withPublicUntil(infos, until, map[string]bool{"demo": true}, now.Add(3*time.Hour))
	for _, info := range infos {
		if info.Name == "demo" && (!info.Public || !info.PublicUntil.IsZero()) {
			t.Fatalf("shared demo = %+v", info)
		}
	}
}

func TestUntimedPublicHintsOnceWhenCreated(t *testing.T) {
	cfg := timedProject(t) // demo: public 2h, hooks: public forever
	create := func(service string) reconcile.Operation {
		return reconcile.Operation{Kind: reconcile.KindCreate, After: reconcile.Route{Service: service, HTTPSPort: 443, Public: true}}
	}
	plan := reconcile.Plan{Operations: []reconcile.Operation{create("demo"), create("hooks")}}
	hints := untimedPublic(plan, cfg, nil)
	if len(hints) != 1 || !strings.HasPrefix(hints[0], "hooks is now PUBLIC with no time limit") {
		t.Fatalf("hints = %v", hints)
	}
	kept := reconcile.Plan{Operations: []reconcile.Operation{{Kind: reconcile.KindKeep, After: reconcile.Route{Service: "hooks", Public: true}}}}
	if hints := untimedPublic(kept, cfg, nil); len(hints) != 0 {
		t.Fatalf("an existing public route was hinted again: %v", hints)
	}
	if hints := untimedPublic(plan, cfg, map[string]bool{"hooks": true}); len(hints) != 0 {
		t.Fatalf("pier share was second-guessed: %v", hints)
	}
}

func TestExpiryDueNeedsAnOwnedPublicRoute(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	st := state.ProjectState{
		Routes:      []state.Route{{Service: "demo", HTTPSPort: 443, Path: "/"}},
		PublicUntil: map[string]time.Time{"demo": now.Add(time.Hour)},
	}
	if st.ExpiryDue(now) || !st.OwnsTimedPublic() {
		t.Fatal("an open window is not due but keeps the daemon up")
	}
	if !st.ExpiryDue(now.Add(time.Hour)) {
		t.Fatal("a closed window with a public route is due")
	}
	st.Routes[0].HTTPSPort = 8443 // expired: the route moved to Serve
	if st.ExpiryDue(now.Add(time.Hour)) || st.OwnsTimedPublic() {
		t.Fatal("once private, nothing is due and the daemon may exit")
	}
}
