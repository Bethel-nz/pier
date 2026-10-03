package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Bethel-nz/pier/internal/config"
	"github.com/Bethel-nz/pier/internal/tailscale"
)

// noFunnelEnv is a healthy tailnet device whose Funnel is not authorized.
func noFunnelEnv() *fakeEnv {
	env := newEnv()
	env.caps.Funnel = false
	return env
}

func privateOnly(env *fakeEnv) {
	env.raw.Services = map[string]config.Service{
		"web": {Target: "localhost:3000"},
		"api": {Target: "localhost:4000", Path: "/api"},
	}
}

func TestPrivateOnlyProjectDoesNotNeedFunnel(t *testing.T) {
	t.Run("plan", func(t *testing.T) {
		env := noFunnelEnv()
		privateOnly(env)

		result, err := env.service().Plan(context.Background(), PlanRequest{Start: env.project.Root})
		if err != nil {
			t.Fatalf("Plan() = %v, want a plan without Funnel", err)
		}
		if result.TailscaleSkipped != "" || len(result.Plan.Operations) != 2 {
			t.Fatalf("Plan() skipped=%q ops=%d, want 2 Serve creates", result.TailscaleSkipped, len(result.Plan.Operations))
		}
	})

	t.Run("up", func(t *testing.T) {
		env := noFunnelEnv()
		privateOnly(env)
		env.after = env.desiredRoutes()

		if _, err := env.service().Up(context.Background(), UpRequest{Start: env.project.Root}); err != nil {
			t.Fatalf("Up() = %v, want private routes applied without Funnel", err)
		}
		if len(env.applyCalls) != 2 || env.saved == nil || len(env.saved.Routes) != 2 {
			t.Fatalf("Up() applied %d, saved %#v, want 2 owned Serve routes", len(env.applyCalls), env.saved)
		}
	})
}

func TestPublicServiceNeedsFunnelBeforeMutation(t *testing.T) {
	assertFunnelErr := func(t *testing.T, env *fakeEnv, err error) {
		t.Helper()
		var prereq *PrerequisiteError
		if !errors.As(err, &prereq) || !errors.Is(err, tailscale.ErrFunnelUnauthorized) {
			t.Fatalf("error = %v, want a Funnel prerequisite error", err)
		}
		if env.mutated || env.saved != nil {
			t.Fatalf("mutated=%v saved=%v, want nothing changed", env.mutated, env.saved != nil)
		}
	}

	t.Run("configured public", func(t *testing.T) {
		env := noFunnelEnv() // webhook is public: true
		_, err := env.service().Up(context.Background(), UpRequest{Start: env.project.Root})
		assertFunnelErr(t, env, err)
	})

	t.Run("configured public plan", func(t *testing.T) {
		env := noFunnelEnv()
		_, err := env.service().Plan(context.Background(), PlanRequest{Start: env.project.Root})
		assertFunnelErr(t, env, err)
	})

	t.Run("runtime share", func(t *testing.T) {
		env := noFunnelEnv()
		privateOnly(env)
		_, err := env.service().Share(context.Background(), ShareRequest{Start: env.project.Root, Service: "api"})
		assertFunnelErr(t, env, err)
	})

	t.Run("saved share override", func(t *testing.T) {
		env := noFunnelEnv()
		privateOnly(env)
		env.state.Overrides = map[string]bool{"api": true}
		_, err := env.service().Up(context.Background(), UpRequest{Start: env.project.Root})
		assertFunnelErr(t, env, err)
	})
}

func TestPublicServiceWithoutFunnelStillServesLocalNames(t *testing.T) {
	env := domainEnv()
	env.caps.Funnel = false
	env.raw.Services["hook"] = config.Service{Target: "localhost:8787", Path: "/hooks", Public: config.PublicFlag(true)}
	local := &fakeLocal{report: liveReport("myapp.local", "api.myapp.local")}
	svc := env.service()
	svc.EnableLocalNames(local)

	result, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Up() = %v, want local names served", err)
	}
	if !strings.Contains(result.TailscaleSkipped, "Funnel") || env.mutated {
		t.Fatalf("skipped=%q mutated=%v, want Tailscale left alone for Funnel", result.TailscaleSkipped, env.mutated)
	}
}

func TestDoctorReportsMissingFunnelWithoutFailingTailscale(t *testing.T) {
	env := noFunnelEnv()

	result, err := env.service().Doctor(context.Background(), DoctorRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Doctor() = %v", err)
	}
	if result.TailscaleErr != nil || result.Capabilities.Funnel || !result.Capabilities.Authenticated {
		t.Fatalf("Doctor() err=%v caps=%+v, want healthy Tailscale without Funnel", result.TailscaleErr, result.Capabilities)
	}
	if got := funnelWarnings(result.Warnings); len(got) != 1 || !strings.Contains(got[0], "webhook") {
		t.Fatalf("Doctor() warnings = %q, want one Funnel warning naming webhook", result.Warnings)
	}

	privateOnly(env)
	result, _ = env.service().Doctor(context.Background(), DoctorRequest{Start: env.project.Root})
	if got := funnelWarnings(result.Warnings); len(got) != 0 {
		t.Fatalf("Doctor() warnings = %q for a private-only project, want no Funnel warning", got)
	}
}

func funnelWarnings(warnings []string) []string {
	var out []string
	for _, warning := range warnings {
		if strings.Contains(warning, "Funnel") {
			out = append(out, warning)
		}
	}
	return out
}
