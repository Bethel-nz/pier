package config

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestThrottleForms(t *testing.T) {
	cases := []struct {
		yaml    string
		want    Shaping
		wantErr string
	}{
		{`throttle: 3g`, throttlePresets["3g"], ""},
		{`throttle: {latency: 300ms, down: 1.5mbit, up: 750kbit}`, Shaping{Latency: 300 * time.Millisecond, Down: 187_500, Up: 93_750}, ""},
		{`throttle: {latency: 1s}`, Shaping{Latency: time.Second}, ""},
		{`throttle: 5g`, Shaping{}, "is not a preset"},
		{`throttle: {down: fast}`, Shaping{}, "must be a rate"},
		{`throttle: {latency: 2m}`, Shaping{}, "up to 60s"},
		{`throttle: {}`, Shaping{}, "needs a preset"},
	}
	for _, c := range cases {
		var service Service
		if err := yaml.Unmarshal([]byte(c.yaml), &service); err != nil {
			t.Fatalf("%s: %v", c.yaml, err)
		}
		got, err := resolveThrottle(service.Throttle)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: err = %v, want %q", c.yaml, err, c.wantErr)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: got %+v, %v; want %+v", c.yaml, got, err, c.want)
		}
	}
}

func TestThrottleRejectsUnknownKeys(t *testing.T) {
	var service Service
	if err := yaml.Unmarshal([]byte("throttle: {latncy: 1s}"), &service); err == nil || !strings.Contains(err.Error(), `no field "latncy"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestCaptureAndTapped(t *testing.T) {
	project, err := Normalize(Config{Version: 1, Name: "p", Services: map[string]Service{
		"api":  {Target: "localhost:3000", Path: "/api", Capture: "24h"},
		"web":  {Target: "localhost:4000", Throttle: &Throttle{Preset: "3g"}},
		"docs": {Target: "localhost:5000", Path: "/docs"},
		"bad":  {Target: "localhost:6000", Path: "/bad", Capture: "10s"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tapped := map[string]bool{}
	for _, service := range project.Services {
		tapped[service.Name] = service.Tapped()
	}
	if !tapped["api"] || !tapped["web"] || tapped["docs"] || tapped["bad"] {
		t.Fatalf("tapped = %v", tapped)
	}
	errs := Validate(project)
	if len(errs) != 1 || errs[0].Field != "capture" || errs[0].Service != "bad" {
		t.Fatalf("errors = %v", errs)
	}
}
