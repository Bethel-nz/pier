package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"pier/internal/config"
	"pier/internal/reconcile"
	"pier/internal/state"
)

func TestPauseRemovesRouteWithoutChangingConfigOrProcess(t *testing.T) {
	env := ownedShareEnv()
	env.after = []reconcile.Route{
		{Service: "web", ProjectID: env.project.ID, HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"},
		{Service: "webhook", ProjectID: env.project.ID, HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true},
	}
	before := env.raw
	svc := env.service()

	result, err := svc.Pause(context.Background(), PauseRequest{Start: env.project.Root, Service: "api"})
	if err != nil {
		t.Fatalf("Pause() error = %v", err)
	}
	if !reflect.DeepEqual(env.raw, before) {
		t.Fatalf("Pause() mutated pier.yaml in memory: %#v", env.raw)
	}
	if env.saved == nil || env.saved.Paused["api"] != true {
		t.Fatalf("Pause() saved paused = %#v, want api=true", env.saved)
	}
	if !result.Service.Paused {
		t.Fatalf("Pause() service = %#v, want paused", result.Service)
	}
	if !hasKind(operationKinds(result.Plan), reconcile.KindDelete) {
		t.Fatalf("Pause() operations = %#v, want delete", operationKinds(result.Plan))
	}
}

func TestPauseWithoutLiveRouteStillPersists(t *testing.T) {
	env := ownedShareEnv()
	env.actual = []reconcile.Route{
		{Service: "web", ProjectID: env.project.ID, HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"},
		{Service: "webhook", ProjectID: env.project.ID, HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true},
	}
	env.state.Routes = []state.Route{
		{Service: "web", HTTPSPort: 8443, Path: "/"},
		{Service: "webhook", HTTPSPort: 443, Path: "/hooks"},
	}
	svc := env.service()

	if _, err := svc.Pause(context.Background(), PauseRequest{Start: env.project.Root, Service: "api"}); err != nil {
		t.Fatalf("Pause() error = %v", err)
	}
	if env.mutated {
		t.Fatal("Pause() mutated Tailscale for a service that was not routed")
	}
	if env.saved == nil || env.saved.Paused["api"] != true {
		t.Fatalf("Pause() saved paused = %#v, want api=true", env.saved)
	}
}

func TestResumeRestoresPausedRoute(t *testing.T) {
	env := ownedShareEnv()
	env.state.Paused = map[string]bool{"api": true}
	env.actual = []reconcile.Route{
		{Service: "web", ProjectID: env.project.ID, HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"},
		{Service: "webhook", ProjectID: env.project.ID, HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true},
	}
	env.state.Routes = []state.Route{
		{Service: "web", HTTPSPort: 8443, Path: "/"},
		{Service: "webhook", HTTPSPort: 443, Path: "/hooks"},
	}
	env.after = []reconcile.Route{
		{Service: "api", ProjectID: env.project.ID, HTTPSPort: 8443, Path: "/api", Target: "http://127.0.0.1:4000"},
		{Service: "web", ProjectID: env.project.ID, HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"},
		{Service: "webhook", ProjectID: env.project.ID, HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true},
	}
	svc := env.service()

	result, err := svc.Resume(context.Background(), ResumeRequest{Start: env.project.Root, Service: "api"})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if env.saved == nil {
		t.Fatal("Resume() did not persist state")
	}
	if env.saved.Paused["api"] {
		t.Fatalf("Resume() kept api paused: %#v", env.saved.Paused)
	}
	if result.Service.Paused {
		t.Fatalf("Resume() service = %#v, want active", result.Service)
	}
	if !hasKind(operationKinds(result.Plan), reconcile.KindCreate) {
		t.Fatalf("Resume() operations = %#v, want create", operationKinds(result.Plan))
	}
}

func TestUpSkipsPausedServices(t *testing.T) {
	env := newEnv()
	env.state.Paused = map[string]bool{"webhook": true}
	env.after = env.desiredRoutes()
	svc := env.service()

	result, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	for _, info := range result.Services {
		if info.Name == "webhook" && !info.Paused {
			t.Fatalf("Up() webhook = %#v, want paused", info)
		}
	}
	if env.saved != nil {
		for _, route := range env.saved.Routes {
			if route.Service == "webhook" {
				t.Fatalf("Up() owned paused webhook: %#v", env.saved.Routes)
			}
		}
	}
}

func TestOpenPausedService(t *testing.T) {
	env := ownedShareEnv()
	env.state.Paused = map[string]bool{"api": true}
	env.actual = []reconcile.Route{
		{Service: "web", ProjectID: env.project.ID, HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"},
	}
	svc := env.service()

	_, err := svc.Open(context.Background(), OpenRequest{Start: env.project.Root, Service: "api"})
	var paused *ServicePausedError
	if !errors.As(err, &paused) {
		t.Fatalf("Open() error = %v, want *ServicePausedError", err)
	}
}

func TestAddServiceWritesYAMLWithoutTouchingTailscale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pier.yaml")
	original := []byte("version: 1\nname: greppa\nservices:\n  web:\n    target: localhost:3000\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	env := newEnv()
	env.project.Root = dir
	env.project.ConfigPath = path
	env.raw.Services = map[string]config.Service{"web": {Target: "localhost:3000"}}
	svc := env.service()
	svc.addService = config.AddService
	svc.load = func(configPath string) (config.Config, error) {
		env.record("load and validate config")
		return config.Load(configPath)
	}

	result, err := svc.AddService(context.Background(), AddServiceRequest{
		Start:  dir,
		Name:   "api",
		Target: "localhost:4000",
		Path:   "/api",
	})
	if err != nil {
		t.Fatalf("AddService() error = %v", err)
	}
	if env.mutated {
		t.Fatal("AddService() mutated Tailscale")
	}
	if result.Service.Name != "api" || result.Service.Path != "/api" {
		t.Fatalf("AddService() service = %#v", result.Service)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(original[:len("version: 1\nname: greppa\n")], got[:len("version: 1\nname: greppa\n")]) {
		t.Fatalf("AddService() rewrote the document start:\n%s", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	raw, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Services["api"].Target != "localhost:4000" {
		t.Fatalf("written api = %#v", raw.Services["api"])
	}
}

func TestAddServiceRejectsDuplicateName(t *testing.T) {
	env := newEnv()
	svc := env.service()
	_, err := svc.AddService(context.Background(), AddServiceRequest{
		Start:  env.project.Root,
		Name:   "web",
		Target: "localhost:4000",
	})
	var exists *ServiceExistsError
	if !errors.As(err, &exists) {
		t.Fatalf("AddService() error = %v, want *ServiceExistsError", err)
	}
	if env.mutated {
		t.Fatal("AddService() mutated Tailscale for a duplicate")
	}
}
