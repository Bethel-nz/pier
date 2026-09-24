package cli

import (
	"strings"
	"testing"

	"github.com/Bethel-nz/pier/internal/app"
)

func qrServices() []app.ServiceInfo {
	return []app.ServiceInfo{
		{Name: "api", URL: "https://box.ts.net:8443/api", Domain: "api.demo.local", LocalState: "conflict", LANURL: "http://192.168.1.20:4101/"},
		{Name: "web", URL: "https://box.ts.net:8443/", Domain: "demo.local", LocalURL: "https://demo.local/", LocalState: "live", LANURL: "http://192.168.1.20:4100/"},
		{Name: "worker", URL: "https://box.ts.net:8443/worker"},
	}
}

func TestQRTargetPicksTheRightURL(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		choice qrChoice
		want   string
	}{
		{"named service", []string{"web"}, qrChoice{}, "https://demo.local/"},
		{"CA link for phones", nil, qrChoice{ca: true}, "http://demo.local/.pier/"},
		{"tailscale URL on request", []string{"worker"}, qrChoice{tailnet: true}, "https://box.ts.net:8443/worker"},
		{"LAN address", []string{"web"}, qrChoice{lan: true}, "http://192.168.1.20:4100/"},
		{"LAN address while the name is in conflict", []string{"api"}, qrChoice{lan: true}, "http://192.168.1.20:4101/"},
	}
	for _, tc := range cases {
		got, err := qrTarget(qrServices(), tc.args, tc.choice)
		if err != nil || got != tc.want {
			t.Errorf("%s: qrTarget = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestQRTargetExplainsInsteadOfGuessing(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		choice qrChoice
		want   string
	}{
		{"several local services and no name", nil, qrChoice{}, "needs a service name"},
		{"name that is not served", []string{"api"}, qrChoice{}, "not serving api.demo.local right now (conflict); pier qr --lan works meanwhile"},
		{"service without a local name", []string{"worker"}, qrChoice{}, "no .local name"},
		{"no LAN address", []string{"worker"}, qrChoice{lan: true}, "no .local name"},
		{"unknown service", []string{"nope"}, qrChoice{}, "could not find"},
	}
	for _, tc := range cases {
		_, err := qrTarget(qrServices(), tc.args, tc.choice)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
}
