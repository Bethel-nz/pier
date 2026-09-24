package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Bethel-nz/pier/internal/reconcile"
	"github.com/Bethel-nz/pier/internal/state"
)

func TestPublicWarnings(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	actual := []reconcile.Route{
		{HTTPSPort: 443, Path: "/demo", Public: true},  // owned, 3 days old
		{HTTPSPort: 443, Path: "/fresh", Public: true}, // owned, 1 hour old
		{HTTPSPort: 443, Path: "/stray", Public: true}, // nobody's
		{HTTPSPort: 8443, Path: "/", Public: false},    // tailnet only: never a warning
	}
	owners := map[string]owner{
		"https:443:/demo":  {project: "shop", route: state.Route{Service: "demo", Since: now.Add(-72 * time.Hour)}},
		"https:443:/fresh": {project: "shop", route: state.Route{Service: "fresh", Since: now.Add(-time.Hour)}},
	}
	warnings := publicWarnings(actual, owners, "host.ts.net", now, nil)
	joined := strings.Join(warnings, "\n")
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v", warnings)
	}
	if !strings.Contains(joined, "https://host.ts.net/demo (demo in shop) has been PUBLIC for 3d") {
		t.Errorf("missing the stale route: %s", joined)
	}
	if !strings.Contains(joined, "https://host.ts.net/stray is PUBLIC on the internet and no Pier project owns it") {
		t.Errorf("missing the unowned route: %s", joined)
	}

	skipped := publicWarnings(actual, owners, "host.ts.net", now, map[string]bool{"https:443:/stray": true, "https:443:/demo": true})
	if len(skipped) != 0 {
		t.Fatalf("skipped routes still warned: %v", skipped)
	}
}

func TestNextOwnedStampsPublicAndSince(t *testing.T) {
	then := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	now := then.Add(24 * time.Hour)
	prev := []state.Route{{Service: "web", HTTPSPort: 8443, Path: "/", Since: then}}
	ops := []reconcile.Operation{
		{Kind: reconcile.KindKeep, Before: reconcile.Route{HTTPSPort: 8443, Path: "/"}, After: reconcile.Route{Service: "web", HTTPSPort: 8443, Path: "/"}},
		{Kind: reconcile.KindCreate, After: reconcile.Route{Service: "hook", HTTPSPort: 443, Path: "/hook", Public: true}},
	}
	got := nextOwned(prev, ops, now)
	if len(got) != 2 || !got[0].Public || !got[0].Since.Equal(now) || got[1].Public || !got[1].Since.Equal(then) {
		t.Fatalf("owned = %+v; a kept route keeps its time, a new one is stamped", got)
	}
}
