package config

import (
	"strings"
	"testing"
)

func TestTCPServices(t *testing.T) {
	project, err := Normalize(Config{Version: 1, Name: "p", Services: map[string]Service{
		"db":    {Target: "localhost:5432", Protocol: "tcp"},
		"redis": {Target: "localhost:6379", Protocol: "tcp", Listen: 16379},
		"web":   {Target: "localhost:3000"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ResolvedService{}
	for _, service := range project.Services {
		byName[service.Name] = service
	}
	if db := byName["db"]; !db.TCP() || db.HTTPSPort != 5432 || db.Path != "" || db.Target != "tcp://127.0.0.1:5432" {
		t.Fatalf("db = %+v", db)
	}
	if redis := byName["redis"]; redis.HTTPSPort != 16379 {
		t.Fatalf("redis listens on %d, want 16379", redis.HTTPSPort)
	}
	if errs := Validate(project); len(errs) != 0 {
		t.Fatalf("valid project reported %v", errs)
	}
}

func TestTCPRejectsHTTPOnlySettings(t *testing.T) {
	project, err := Normalize(Config{Version: 1, Name: "p", Services: map[string]Service{
		"db":    {Target: "localhost:5432", Protocol: "tcp", Path: "/db", Public: PublicFlag(true), Capture: "1h", Throttle: &Throttle{Preset: "3g"}},
		"dup":   {Target: "localhost:5433", Protocol: "tcp", Listen: 5432},
		"https": {Target: "localhost:5434", Protocol: "tcp", Listen: 443},
		"web":   {Target: "localhost:3000", Listen: 3000},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range Validate(project) {
		got[e.Service+"."+e.Field] = e.Message
	}
	for key, want := range map[string]string{
		"db.path":      "reached by port",
		"db.public":    "tailnet-only",
		"db.capture":   "HTTP services only",
		"db.throttle":  "HTTP services only",
		"dup.listen":   "already used by TCP service",
		"https.listen": "where Pier serves HTTP",
		"web.listen":   "TCP services only",
	} {
		if !strings.Contains(got[key], want) {
			t.Errorf("%s = %q, want it to mention %q", key, got[key], want)
		}
	}
}
