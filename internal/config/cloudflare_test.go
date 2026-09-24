package config

import (
	"strings"
	"testing"
)

func TestCloudflareHostnames(t *testing.T) {
	project, err := Normalize(Config{Version: 1, Name: "My App", Services: map[string]Service{
		"web": {Target: "localhost:3000", Cloudflare: "App.Example.com."},
		"api": {Target: "localhost:4000", Path: "/api", Cloudflare: "api.example.com"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if errs := Validate(project); len(errs) != 0 {
		t.Fatalf("valid project reported %v", errs)
	}
	if !project.HasCloudflare() {
		t.Fatal("HasCloudflare = false")
	}
	for _, service := range project.Services {
		if service.Name == "web" && service.Cloudflare != "app.example.com" {
			t.Fatalf("web hostname = %q", service.Cloudflare)
		}
	}
	if name := project.TunnelName(); name != "pier-my-app" {
		t.Fatalf("tunnel name = %q", name)
	}
}

func TestCloudflareRejectsBadHostnames(t *testing.T) {
	project, err := Normalize(Config{Version: 1, Name: "p", Services: map[string]Service{
		"url":   {Target: "localhost:3001", Cloudflare: "https://app.example.com"},
		"local": {Target: "localhost:3002", Cloudflare: "app.local"},
		"bare":  {Target: "localhost:3003", Cloudflare: "example"},
		"label": {Target: "localhost:3004", Cloudflare: "under_score.example.com"},
		"db":    {Target: "localhost:5432", Protocol: "tcp", Cloudflare: "db.example.com"},
		"one":   {Target: "localhost:3005", Cloudflare: "same.example.com"},
		"two":   {Target: "localhost:3006", Cloudflare: "same.example.com"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range Validate(project) {
		got[e.Service+"."+e.Field] = e.Message
	}
	for key, want := range map[string]string{
		"url.cloudflare":   "not a URL",
		"local.cloudflare": "zone you own",
		"bare.cloudflare":  "include the domain",
		"label.cloudflare": "lowercase letters",
		"db.cloudflare":    "HTTP services only",
		"two.cloudflare":   `service "one"`,
	} {
		if !strings.Contains(got[key], want) {
			t.Errorf("%s = %q, want it to mention %q", key, got[key], want)
		}
	}
}
