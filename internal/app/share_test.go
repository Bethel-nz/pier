package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"pier/internal/reconcile"
	"pier/internal/state"
)

func TestShareStoresOverrideWithoutChangingConfig(t *testing.T) {
	env := ownedShareEnv()
	before := env.raw
	svc := env.service()

	result, err := svc.Share(context.Background(), ShareRequest{Start: env.project.Root, Service: "api"})
	if err != nil {
		t.Fatalf("Share() error = %v", err)
	}
	if !reflect.DeepEqual(env.raw, before) {
		t.Fatalf("Share() mutated pier.yaml in memory: %#v", env.raw)
	}
	if env.saved == nil || env.saved.Overrides["api"] != true {
		t.Fatalf("Share() saved overrides = %#v, want api=true", env.saved)
	}
	if result.Service.URL != "https://host.ts.net/api" || !result.Service.Public {
		t.Fatalf("Share() service = %#v, want public URL", result.Service)
	}

	kinds := operationKinds(result.Plan)
	if !hasKind(kinds, reconcile.KindDelete) || !hasKind(kinds, reconcile.KindCreate) {
		t.Fatalf("Share() operations = %#v, want delete+create for the listener move", kinds)
	}
}

func TestShareLeavesPierYAMLBytesUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pier.yaml")
	original := []byte("version: 1\nname: greppa\nservices:\n  web:\n    target: localhost:3000\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	env := ownedShareEnv()
	env.project.Root = dir
	env.project.ConfigPath = path
	svc := env.service()

	if _, err := svc.Share(context.Background(), ShareRequest{Start: dir, Service: "api"}); err != nil {
		t.Fatalf("Share() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(got, original) {
		t.Fatalf("pier.yaml changed:\n%s", got)
	}
}

func TestUnshareRemovesOverrideAndRestoresConfiguredAccess(t *testing.T) {
	env := ownedShareEnv()
	env.state.Overrides = map[string]bool{"api": true}
	env.actual = []reconcile.Route{
		{Service: "api", ProjectID: env.project.ID, HTTPSPort: 443, Path: "/api", Target: "http://127.0.0.1:4000", Public: true},
		{Service: "web", ProjectID: env.project.ID, HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"},
		{Service: "webhook", ProjectID: env.project.ID, HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true},
	}
	env.state.Routes = []state.Route{
		{Service: "api", HTTPSPort: 443, Path: "/api"},
		{Service: "web", HTTPSPort: 8443, Path: "/"},
		{Service: "webhook", HTTPSPort: 443, Path: "/hooks"},
	}
	env.after = []reconcile.Route{
		{Service: "api", ProjectID: env.project.ID, HTTPSPort: 8443, Path: "/api", Target: "http://127.0.0.1:4000"},
		{Service: "web", ProjectID: env.project.ID, HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"},
		{Service: "webhook", ProjectID: env.project.ID, HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true},
	}
	svc := env.service()

	result, err := svc.Unshare(context.Background(), UnshareRequest{Start: env.project.Root, Service: "api"})
	if err != nil {
		t.Fatalf("Unshare() error = %v", err)
	}
	if env.saved == nil {
		t.Fatal("Unshare() did not persist state")
	}
	if _, ok := env.saved.Overrides["api"]; ok {
		t.Fatalf("Unshare() kept api override: %#v", env.saved.Overrides)
	}
	if result.Service.Public || result.Service.URL != "https://host.ts.net:8443/api" {
		t.Fatalf("Unshare() service = %#v, want configured tailnet URL", result.Service)
	}
}

func TestShareAndUnshareAreIdempotent(t *testing.T) {
	env := ownedShareEnv()
	env.actual = env.desiredRoutes()
	env.state.Routes = []state.Route{
		{Service: "api", HTTPSPort: 8443, Path: "/api"},
		{Service: "web", HTTPSPort: 8443, Path: "/"},
		{Service: "webhook", HTTPSPort: 443, Path: "/hooks"},
	}
	svc := env.service()

	if _, err := svc.Share(context.Background(), ShareRequest{Start: env.project.Root, Service: "webhook"}); err != nil {
		t.Fatalf("Share(already public) error = %v", err)
	}
	if env.mutated {
		t.Fatal("Share(already public) mutated Tailscale")
	}

	env = ownedShareEnv()
	env.actual = env.desiredRoutes()
	env.state.Routes = []state.Route{
		{Service: "api", HTTPSPort: 8443, Path: "/api"},
		{Service: "web", HTTPSPort: 8443, Path: "/"},
		{Service: "webhook", HTTPSPort: 443, Path: "/hooks"},
	}
	svc = env.service()
	if _, err := svc.Unshare(context.Background(), UnshareRequest{Start: env.project.Root, Service: "api"}); err != nil {
		t.Fatalf("Unshare(no override) error = %v", err)
	}
	if env.mutated {
		t.Fatal("Unshare(no override) mutated Tailscale")
	}
}

func TestShareRollsBackOverrideWhenApplyFails(t *testing.T) {
	env := ownedShareEnv()
	env.applyErr = errors.New("apply failed")
	svc := env.service()

	_, err := svc.Share(context.Background(), ShareRequest{Start: env.project.Root, Service: "api"})
	if err == nil {
		t.Fatal("Share() error = nil, want apply failure")
	}
	if env.saved != nil {
		t.Fatalf("Share() persisted state after apply failure: %#v", env.saved)
	}
	if env.state.Overrides["api"] {
		t.Fatalf("Share() left in-memory override after failure: %#v", env.state.Overrides)
	}
}

func ownedShareEnv() *fakeEnv {
	env := newEnv()
	env.actual = env.desiredRoutes()
	env.state.Routes = []state.Route{
		{Service: "api", HTTPSPort: 8443, Path: "/api"},
		{Service: "web", HTTPSPort: 8443, Path: "/"},
		{Service: "webhook", HTTPSPort: 443, Path: "/hooks"},
	}
	env.after = []reconcile.Route{
		{Service: "api", ProjectID: env.project.ID, HTTPSPort: 443, Path: "/api", Target: "http://127.0.0.1:4000", Public: true},
		{Service: "web", ProjectID: env.project.ID, HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"},
		{Service: "webhook", ProjectID: env.project.ID, HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true},
	}
	return env
}

func operationKinds(plan reconcile.Plan) []reconcile.Kind {
	var kinds []reconcile.Kind
	for _, op := range plan.Operations {
		if op.Kind != reconcile.KindKeep {
			kinds = append(kinds, op.Kind)
		}
	}
	return kinds
}

func hasKind(kinds []reconcile.Kind, want reconcile.Kind) bool {
	for _, kind := range kinds {
		if kind == want {
			return true
		}
	}
	return false
}

func bytesEqual(a, b []byte) bool {
	return string(a) == string(b)
}
