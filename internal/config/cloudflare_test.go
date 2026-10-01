package config

import (
	"strings"
	"testing"
)

func TestCloudflareProvider(t *testing.T) {
	project, err := Normalize(Config{Version: 1, Name: "My App", Domain: "Example.com.", Defaults: Defaults{Public: true}, Services: map[string]Service{
		"web":   {Target: "localhost:3000", Provider: "cloudflare"},
		"api":   {Target: "localhost:4000", Provider: "Cloudflare", Hostname: "api-v2"},
		"full":  {Target: "localhost:4001", Provider: "cloudflare", Hostname: "status.example.com"},
		"admin": {Target: "localhost:5000"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if errs := Validate(project); len(errs) != 0 {
		t.Fatalf("valid project reported %v", errs)
	}
	want := map[string]string{"web": "web.example.com", "api": "api-v2.example.com", "full": "status.example.com", "admin": ""}
	for _, service := range project.Services {
		if service.Cloudflare != want[service.Name] {
			t.Errorf("%s hostname = %q, want %q", service.Name, service.Cloudflare, want[service.Name])
		}
		cloudflare := want[service.Name] != ""
		if service.OnTailscale() == cloudflare {
			t.Errorf("%s on Tailscale = %v", service.Name, service.OnTailscale())
		}
		// Cloudflare services are public there, not through Tailscale Funnel.
		if cloudflare && service.Public {
			t.Errorf("%s inherited defaults.public", service.Name)
		}
	}
	if !project.HasCloudflare() || project.TunnelName() != "pier-my-app" {
		t.Fatalf("HasCloudflare = %v, tunnel = %q", project.HasCloudflare(), project.TunnelName())
	}
}

func TestCloudflareProviderErrors(t *testing.T) {
	project, err := Normalize(Config{Version: 1, Name: "p", Domain: "example.com", Services: map[string]Service{
		"odd":   {Target: "localhost:3001", Provider: "ngrok"},
		"label": {Target: "localhost:3002", Provider: "cloudflare", Hostname: "under_score"},
		"db":    {Target: "localhost:5432", Protocol: "tcp", Provider: "cloudflare"},
		"one":   {Target: "localhost:3005", Provider: "cloudflare", Hostname: "same"},
		"two":   {Target: "localhost:3006", Provider: "cloudflare", Hostname: "same"},
		"both":  {Target: "localhost:3007", Provider: "cloudflare", Path: "/both", Public: PublicFlag(true)},
		"tail":  {Target: "localhost:3008", Hostname: "tail"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range Validate(project) {
		got[e.Service+"."+e.Field] = e.Message
	}
	for key, want := range map[string]string{
		"odd.provider":   "tailscale or cloudflare",
		"label.hostname": "lowercase letters",
		"db.provider":    "HTTP services only",
		"two.hostname":   `service "one"`,
		"both.path":      "Tailscale setting",
		"both.public":    "always public",
		"tail.hostname":  "provider: cloudflare only",
	} {
		if !strings.Contains(got[key], want) {
			t.Errorf("%s = %q, want it to mention %q", key, got[key], want)
		}
	}
}

func TestCloudflareNeedsADomain(t *testing.T) {
	for domain, want := range map[string]string{
		"":               "add domain:",
		"example":        "full domain",
		"https://ex.com": "not a URL",
		"myapp.local":    "Cloudflare account",
	} {
		project, err := Normalize(Config{Version: 1, Name: "p", Domain: domain, Services: map[string]Service{
			"web": {Target: "localhost:3000", Provider: "cloudflare"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		errs := Validate(project)
		if len(errs) != 1 || !strings.Contains(errs[0].Message, want) {
			t.Errorf("domain %q: errors = %v, want one mentioning %q", domain, errs, want)
		}
	}
}
