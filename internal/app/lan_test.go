package app

import (
	"testing"

	"github.com/Bethel-nz/pier/internal/config"
)

func TestLocalDomainsGetStableLANPorts(t *testing.T) {
	busy := map[int]bool{4100: true} // another program holds 4100
	previous := lanPortFree
	lanPortFree = func(port int) bool { return !busy[port] }
	t.Cleanup(func() { lanPortFree = previous })

	cfg, err := config.Normalize(config.Config{Version: 1, Name: "p", Services: map[string]config.Service{
		"api": {Target: "localhost:4000", Path: "/api", Domain: "api.myapp.local"},
		"web": {Target: "localhost:3000", Domain: "myapp.local"},
		"job": {Target: "localhost:5000", Path: "/job"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	first := localDomains(cfg, nil, nil)
	ports := map[string]int{}
	for _, domain := range first {
		ports[domain.Service] = domain.LANPort
	}
	if len(first) != 2 || ports["api"] != 4101 || ports["web"] != 4102 {
		t.Fatalf("ports = %v; want 4101 and 4102, skipping the busy 4100", ports)
	}

	busy[4101], busy[4102] = true, true // now held by Pier itself
	again := localDomains(cfg, nil, first)
	if !sameDomains(first, again) {
		t.Fatalf("a second pier up moved LAN ports: %v -> %v", first, again)
	}

	cfg.Local.LAN = false
	for _, domain := range localDomains(cfg, nil, first) {
		if domain.LANPort != 0 {
			t.Fatalf("local.lan: false still got a LAN port: %+v", domain)
		}
	}
}
