package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Bethel-nz/pier/internal/config"
	"github.com/Bethel-nz/pier/internal/reconcile"
	"github.com/Bethel-nz/pier/internal/state"
)

// partialEnv plans two creates and fails the second.
func partialEnv() *fakeEnv {
	env := newEnv()
	env.raw.Services = map[string]config.Service{
		"web": {Target: "localhost:3000"},
		"api": {Target: "localhost:4000", Path: "/api"},
	}
	env.applyErr = errors.New("serve failed")
	env.applyFailAt = 2
	return env
}

func TestPartialCreateKeepsOwnershipOfObservedRoute(t *testing.T) {
	env := partialEnv()
	// Only the first create shows up in Tailscale after the failure.
	env.after = nil
	svc := env.service()
	svc.build = func(desired, actual, owned []reconcile.Route, force bool) reconcile.Plan {
		plan := reconcile.Build(desired, actual, owned, force)
		env.after = []reconcile.Route{plan.Operations[0].After}
		return plan
	}

	_, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root})
	var applyErr *ApplyError
	if !errors.As(err, &applyErr) || !strings.Contains(err.Error(), "serve failed") {
		t.Fatalf("Up() error = %v, want the apply failure", err)
	}
	if env.saved == nil || len(env.saved.Routes) != 1 {
		t.Fatalf("saved = %#v, want the one created route owned", env.saved)
	}
	created := env.applyCalls[0].After
	if got := ownedRoute(env.saved.Routes[0]).Key(); got != created.Key() {
		t.Fatalf("owned %s, want %s", got, created.Key())
	}
	if env.saved.Overrides != nil || env.saved.Path != env.project.Root {
		t.Fatalf("saved %#v, want only ownership recorded", env.saved)
	}

	// A later pier down removes the route made before the failure.
	down := newEnv()
	down.raw = env.raw
	down.state = *env.saved
	down.actual = env.after
	down.after = []reconcile.Route{}
	if _, err := down.service().Down(context.Background(), DownRequest{Start: down.project.Root}); err != nil {
		t.Fatalf("Down() = %v", err)
	}
	if len(down.applyCalls) != 1 || down.applyCalls[0].Kind != reconcile.KindDelete || down.applyCalls[0].Before.Key() != created.Key() {
		t.Fatalf("Down() mutations = %#v, want the orphan deleted", down.applyCalls)
	}
}

func TestPartialDeleteDropsOwnershipOfRemovedRoute(t *testing.T) {
	env := newEnv()
	web := reconcile.Route{Service: "web", ProjectID: env.project.ID, HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"}
	api := reconcile.Route{Service: "api", ProjectID: env.project.ID, HTTPSPort: 8443, Path: "/api", Target: "http://127.0.0.1:4000"}
	env.actual = []reconcile.Route{api, web}
	env.state.Routes = []state.Route{
		{Service: "api", HTTPSPort: 8443, Path: "/api"},
		{Service: "web", HTTPSPort: 8443, Path: "/"},
	}
	env.applyErr = errors.New("serve off failed")
	env.applyFailAt = 2
	svc := env.service()
	svc.build = func(desired, actual, owned []reconcile.Route, force bool) reconcile.Plan {
		plan := reconcile.Build(desired, actual, owned, force)
		// The first delete went through; the second route is still live.
		first := plan.Operations[0].Before.Key()
		for _, route := range env.actual {
			if route.Key() != first {
				env.after = append(env.after, route)
			}
		}
		return plan
	}

	_, err := svc.Down(context.Background(), DownRequest{Start: env.project.Root})
	var applyErr *ApplyError
	if !errors.As(err, &applyErr) {
		t.Fatalf("Down() error = %v, want the apply failure", err)
	}
	if env.saved == nil || len(env.saved.Routes) != 1 {
		t.Fatalf("saved = %#v, want one route still owned", env.saved)
	}
	if got, deleted := ownedRoute(env.saved.Routes[0]).Key(), env.applyCalls[0].Before.Key(); got == deleted {
		t.Fatalf("still own %s, which was deleted", got)
	}
}

func TestPartialApplyDoesNotClaimMismatchedRoute(t *testing.T) {
	env := partialEnv()
	svc := env.service()
	svc.build = func(desired, actual, owned []reconcile.Route, force bool) reconcile.Plan {
		plan := reconcile.Build(desired, actual, owned, force)
		wrong := plan.Operations[0].After
		wrong.Target = "http://127.0.0.1:9999" // something else answers on that path
		env.after = []reconcile.Route{wrong}
		return plan
	}

	_, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root})
	if err == nil {
		t.Fatal("Up() error = nil, want the apply failure")
	}
	if env.saved != nil {
		t.Fatalf("saved = %#v, want nothing claimed", env.saved)
	}
}

func TestPartialApplyKeepsApplyErrorWhenSaveFails(t *testing.T) {
	env := partialEnv()
	env.saveErr = errors.New("disk full")
	svc := env.service()
	svc.build = func(desired, actual, owned []reconcile.Route, force bool) reconcile.Plan {
		plan := reconcile.Build(desired, actual, owned, force)
		env.after = []reconcile.Route{plan.Operations[0].After}
		return plan
	}

	_, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root})
	var applyErr *ApplyError
	if !errors.As(err, &applyErr) || !errors.Is(err, env.applyErr) {
		t.Fatalf("Up() error = %v, want the apply failure first", err)
	}
	if !errors.Is(err, env.saveErr) {
		t.Fatalf("Up() error = %v, want the save failure kept too", err)
	}
	if !strings.HasPrefix(err.Error(), applyErr.Error()) {
		t.Fatalf("Up() error = %q, want it to lead with the apply failure", err)
	}
}
