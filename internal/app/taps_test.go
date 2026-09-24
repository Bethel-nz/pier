package app

import (
	"testing"

	"github.com/Bethel-nz/pier/internal/config"
	"github.com/Bethel-nz/pier/internal/project"
	"github.com/Bethel-nz/pier/internal/state"
)

func tappedProject(t *testing.T) config.Project {
	t.Helper()
	cfg, err := config.Normalize(config.Config{Version: 1, Name: "p", Services: map[string]config.Service{
		"web":   {Target: "localhost:3000", Throttle: &config.Throttle{Preset: "3g"}},
		"hooks": {Target: "localhost:8787", Path: "/hooks", Capture: "24h"},
		"docs":  {Target: "localhost:5000", Path: "/docs"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestTailscaleRoutesGoThroughTaps(t *testing.T) {
	cfg := tappedProject(t)
	taps, err := assignTaps(cfg, nil, nil)
	if err != nil || len(taps) != 2 {
		t.Fatalf("taps = %+v, %v", taps, err)
	}
	byService := map[string]state.Tap{}
	for _, tap := range taps {
		byService[tap.Service] = tap
	}
	if byService["hooks"].CaptureSeconds != 24*3600 || byService["hooks"].Throttle != nil {
		t.Fatalf("hooks tap = %+v", byService["hooks"])
	}
	if web := byService["web"]; web.Throttle == nil || web.Throttle.LatencyMS != 560 || web.Target != "http://127.0.0.1:3000" {
		t.Fatalf("web tap = %+v", web)
	}

	routes := desiredRoutes(project.Context{ID: "p"}, cfg, nil, nil, taps)
	targets := map[string]string{}
	for _, route := range routes {
		targets[route.Service] = route.Target
	}
	if targets["web"] != byService["web"].URL() || targets["hooks"] != byService["hooks"].URL() || targets["docs"] != "http://127.0.0.1:5000" {
		t.Fatalf("targets = %v", targets)
	}

	again, _ := assignTaps(cfg, taps, nil)
	if !sameTaps(taps, again) {
		t.Fatal("a second pier up moved the tap ports, which would rewrite Tailscale routes")
	}

	paused, _ := assignTaps(cfg, taps, map[string]bool{"web": true})
	if len(paused) != 1 || paused[0].Service != "hooks" {
		t.Fatalf("a paused service kept its tap: %+v", paused)
	}
}
