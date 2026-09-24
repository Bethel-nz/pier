package config

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestParseSpan(t *testing.T) {
	cases := map[string]time.Duration{
		"2m":       2 * time.Minute,
		"2min":     2 * time.Minute,
		"90s":      90 * time.Second,
		"1h30m":    90 * time.Minute,
		"1hr":      time.Hour,
		"1 hour":   time.Hour,
		"24h":      24 * time.Hour,
		"24hrs":    24 * time.Hour,
		"7d":       7 * 24 * time.Hour,
		"3 days":   3 * 24 * time.Hour,
		"1.5d":     36 * time.Hour,
		"2w":       14 * 24 * time.Hour,
		" 30 MIN ": 30 * time.Minute,
	}
	for text, want := range cases {
		if got, err := ParseSpan(text); err != nil || got != want {
			t.Errorf("ParseSpan(%q) = %v, %v; want %v", text, got, err, want)
		}
	}
	for _, bad := range []string{"", "soon", "5 fortnights", "d"} {
		if _, err := ParseSpan(bad); err == nil {
			t.Errorf("ParseSpan(%q) should fail", bad)
		}
	}
}

func TestCaptureAndPublicAcceptEverydaySpans(t *testing.T) {
	if keep, err := resolveCapture("7d"); err != nil || keep != 7*24*time.Hour {
		t.Fatalf("capture 7d = %v, %v", keep, err)
	}
	if _, err := resolveCapture("31d"); err == nil {
		t.Fatal("capture over 30 days should fail")
	}
	var service Service
	if err := yaml.Unmarshal([]byte("public: 2hr"), &service); err != nil {
		t.Fatal(err)
	}
	if !service.Public.On || service.Public.For != 2*time.Hour || service.Public.problem() != "" {
		t.Fatalf("public 2hr = %+v", service.Public)
	}
}
