package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Bethel-nz/pier/internal/cloudflare"
	"github.com/Bethel-nz/pier/internal/config"
	"github.com/Bethel-nz/pier/internal/localname"
	"github.com/Bethel-nz/pier/internal/state"
)

type fakeTunnels struct {
	missing  bool
	loggedIn bool
	creds    string
	calls    []string
	routeErr error
}

func (f *fakeTunnels) Binary() (string, error) {
	if f.missing {
		return "", cloudflare.ErrNotInstalled
	}
	return "/usr/local/bin/cloudflared", nil
}

func (f *fakeTunnels) LoggedIn() bool { return f.loggedIn }

func (f *fakeTunnels) Login(context.Context) error {
	f.calls = append(f.calls, "login")
	f.loggedIn = true
	return nil
}

func (f *fakeTunnels) EnsureTunnel(_ context.Context, name string) (cloudflare.Tunnel, bool, error) {
	f.calls = append(f.calls, "ensure "+name)
	return cloudflare.Tunnel{ID: "6f1c", Name: name, Credentials: f.creds}, true, nil
}

func (f *fakeTunnels) RouteDNS(_ context.Context, id, host string, overwrite bool) error {
	call := "route " + id + " " + host
	if overwrite {
		call += " --overwrite"
	}
	f.calls = append(f.calls, call)
	return f.routeErr
}

func cloudflareEnv(t *testing.T) (*fakeEnv, *fakeTunnels, *fakeLocal) {
	t.Helper()
	env := newEnv()
	env.raw.Domain = "example.com"
	env.raw.Services = map[string]config.Service{
		"web": {Target: "localhost:3000", Provider: "cloudflare", Hostname: "app"},
		"api": {Target: "localhost:4000", Provider: "cloudflare", Throttle: &config.Throttle{Preset: "3g"}},
		// The only service on Tailscale.
		"docs": {Target: "localhost:5000", Path: "/docs"},
	}
	// api's tap keeps its port, so Tailscale's routes are known in advance.
	env.state.Taps = []state.Tap{{Service: "api", Target: "http://127.0.0.1:4000", Port: 45001}}
	env.after = env.desiredRoutes()
	creds := filepath.Join(t.TempDir(), "6f1c.json")
	if err := os.WriteFile(creds, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	tunnels := &fakeTunnels{creds: creds}
	report := localname.Report{Running: true, Tunnels: map[string]localname.TunnelStatus{
		env.project.ID: {Project: env.project.ID, Name: "pier-greppa", State: localname.TunnelConnected},
	}}
	return env, tunnels, &fakeLocal{report: report}
}

func (e *fakeEnv) cloudflareService(tunnels Tunnels, local LocalNames) *Service {
	svc := e.service()
	svc.tunnels = tunnels
	svc.EnableLocalNames(local)
	return svc
}

func TestUpSetsUpTheTunnelOnce(t *testing.T) {
	env, tunnels, local := cloudflareEnv(t)
	svc := env.cloudflareService(tunnels, local)

	result, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Up() = %v", err)
	}
	want := []string{"login", "ensure pier-greppa", "route 6f1c api.example.com", "route 6f1c app.example.com"}
	if !reflect.DeepEqual(tunnels.calls, want) {
		t.Fatalf("cloudflared calls = %v, want %v", tunnels.calls, want)
	}
	setup := result.Cloudflare
	if !setup.LoggedIn || !setup.Created || setup.Tunnel != "pier-greppa" || len(setup.Routed) != 2 {
		t.Fatalf("setup = %+v", setup)
	}
	saved := env.saved.Tunnel
	if saved == nil || saved.ID != "6f1c" || saved.Binary != "/usr/local/bin/cloudflared" || len(saved.Hosts) != 2 {
		t.Fatalf("saved tunnel = %+v", saved)
	}
	// The throttled service goes through its tap, the other straight to its target.
	if api := saved.Hosts[0]; api.Service != "api" || api.Target != env.saved.Taps[0].URL() {
		t.Fatalf("api host = %+v, taps = %+v", api, env.saved.Taps)
	}
	if web := saved.Hosts[1]; web.Target != "http://127.0.0.1:3000" {
		t.Fatalf("web host = %+v", web)
	}
	if len(local.roots) != 1 {
		t.Fatalf("the daemon was not asked to serve the tunnel: syncs = %v", local.syncs)
	}
	for _, info := range result.Services {
		if info.Cloudflare == "" {
			continue
		}
		if info.CloudflareURL != "https://"+info.Cloudflare+"/" || info.CloudflareState != localname.TunnelConnected || info.URL != "" {
			t.Fatalf("%s cloudflare = %q (%s), tailnet URL %q", info.Name, info.CloudflareURL, info.CloudflareState, info.URL)
		}
	}
	// Tailscale serves only the service without a cloudflare: hostname.
	if len(env.saved.Routes) != 1 || env.saved.Routes[0].Service != "docs" {
		t.Fatalf("owned Tailscale routes = %+v, want docs only", env.saved.Routes)
	}

	// The next pier up has nothing to ask Cloudflare.
	env.state = *env.saved
	tunnels.calls = nil
	if _, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root}); err != nil {
		t.Fatal(err)
	}
	if len(tunnels.calls) != 0 {
		t.Fatalf("second pier up called cloudflared: %v", tunnels.calls)
	}
}

func TestDownKeepsTheTunnelButServesNothing(t *testing.T) {
	env, tunnels, local := cloudflareEnv(t)
	svc := env.cloudflareService(tunnels, local)
	if _, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root}); err != nil {
		t.Fatal(err)
	}
	env.state = *env.saved
	routes := env.after
	env.after = nil // pier down removes them
	down, err := svc.Down(context.Background(), DownRequest{Start: env.project.Root})
	if err != nil {
		t.Fatal(err)
	}
	if down.TunnelStopped != "pier-greppa" {
		t.Fatalf("TunnelStopped = %q", down.TunnelStopped)
	}
	env.after, env.actual, env.applyRecorded = routes, nil, false // Tailscale as pier down left it
	if tunnel := env.saved.Tunnel; tunnel == nil || tunnel.ID != "6f1c" || tunnel.Serving() {
		t.Fatalf("after down, tunnel = %+v; want it kept with no hostnames", tunnel)
	}

	// Up again: the same tunnel, only the DNS routes are checked again.
	taps := env.state.Taps
	env.state = *env.saved
	env.state.Taps = taps // keep api's tap port, which the expected routes use
	tunnels.calls = nil
	if _, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root}); err != nil {
		t.Fatal(err)
	}
	want := []string{"route 6f1c api.example.com", "route 6f1c app.example.com"}
	if !reflect.DeepEqual(tunnels.calls, want) {
		t.Fatalf("calls = %v, want %v", tunnels.calls, want)
	}
}

func TestUpWithCloudflareNeedsNoTailscale(t *testing.T) {
	env, tunnels, local := cloudflareEnv(t)
	env.checkErr = errors.New("Tailscale is not installed or is not available on PATH")
	tunnels.loggedIn = true
	result, err := env.cloudflareService(tunnels, local).Up(context.Background(), UpRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Up() = %v", err)
	}
	if result.TailscaleSkipped == "" || !env.saved.Tunnel.Serving() {
		t.Fatalf("skipped = %q, tunnel = %+v", result.TailscaleSkipped, env.saved.Tunnel)
	}
}

func TestUpExplainsCloudflareProblems(t *testing.T) {
	env, tunnels, local := cloudflareEnv(t)
	tunnels.missing = true
	_, err := env.cloudflareService(tunnels, local).Up(context.Background(), UpRequest{Start: env.project.Root})
	var prereq *PrerequisiteError
	if !errors.As(err, &prereq) || !errors.Is(err, cloudflare.ErrNotInstalled) {
		t.Fatalf("Up() = %v, want cloudflared not installed", err)
	}
	if env.saved != nil {
		t.Fatal("state was saved although the tunnel could not be set up")
	}

	env, tunnels, local = cloudflareEnv(t)
	tunnels.loggedIn = true
	tunnels.routeErr = errors.New("api.example.com " + cloudflare.ErrRecordExists.Error())
	if _, err := env.cloudflareService(tunnels, local).Up(context.Background(), UpRequest{Start: env.project.Root, Force: true}); err == nil {
		t.Fatal("Up() succeeded although routing failed")
	}
	if last := tunnels.calls[len(tunnels.calls)-1]; !strings.HasSuffix(last, "--overwrite") {
		t.Fatalf("pier up --force routed with %q, want --overwrite", last)
	}
}

func TestPausedServiceLeavesTheTunnel(t *testing.T) {
	env, tunnels, local := cloudflareEnv(t)
	tunnels.loggedIn = true
	svc := env.cloudflareService(tunnels, local)
	if _, err := svc.Pause(context.Background(), PauseRequest{Start: env.project.Root, Service: "web"}); err != nil {
		t.Fatal(err)
	}
	hosts := env.saved.Tunnel.Hosts
	if len(hosts) != 1 || hosts[0].Service != "api" {
		t.Fatalf("hosts while web is paused = %+v", hosts)
	}
}

func TestStatusShowsTheTunnel(t *testing.T) {
	env, tunnels, local := cloudflareEnv(t)
	local.report.Tunnels[env.project.ID] = localname.TunnelStatus{Project: env.project.ID, State: localname.TunnelFailed, Detail: "Unauthorized"}
	result, err := env.cloudflareService(tunnels, local).Status(context.Background(), StatusRequest{Start: env.project.Root})
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range result.Services {
		if info.Cloudflare == "" {
			continue
		}
		if info.CloudflareURL != "" || info.CloudflareState != localname.TunnelFailed || info.CloudflareDetail != "Unauthorized" {
			t.Fatalf("%s = %+v", info.Name, info)
		}
		if len(info.Drift) != 0 {
			t.Fatalf("%s reports Tailscale drift for a Cloudflare service: %v", info.Name, info.Drift)
		}
	}
}

func TestShareRefusesACloudflareService(t *testing.T) {
	env, tunnels, local := cloudflareEnv(t)
	_, err := env.cloudflareService(tunnels, local).Share(context.Background(), ShareRequest{Start: env.project.Root, Service: "web"})
	if err == nil || !strings.Contains(err.Error(), "served by Cloudflare") {
		t.Fatalf("Share() = %v", err)
	}
}
