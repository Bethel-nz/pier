package cli

import (
	"strings"
	"testing"

	"github.com/Bethel-nz/pier/internal/app"
)

func qrServices() []app.ServiceInfo {
	return []app.ServiceInfo{
		{Name: "api", URL: "https://box.ts.net:8443/api", Domain: "api.demo.local", LocalState: "conflict"},
		{Name: "web", URL: "https://box.ts.net:8443/", Domain: "demo.local", LocalURL: "https://demo.local/", LocalState: "live"},
		{Name: "worker", URL: "https://box.ts.net:8443/worker"},
	}
}

func TestQRTargetPicksTheRightURL(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		ca      bool
		tailnet bool
		want    string
	}{
		{"named service", []string{"web"}, false, false, "https://demo.local/"},
		{"CA link for phones", nil, true, false, "http://demo.local/.pier/"},
		{"tailscale URL on request", []string{"worker"}, false, true, "https://box.ts.net:8443/worker"},
	}
	for _, tc := range cases {
		got, err := qrTarget(qrServices(), tc.args, tc.ca, tc.tailnet)
		if err != nil || got != tc.want {
			t.Errorf("%s: qrTarget = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestQRTargetExplainsInsteadOfGuessing(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"several local services and no name", nil, "needs a service name"},
		{"name that is not served", []string{"api"}, "not serving api.demo.local right now (conflict)"},
		{"service without a local name", []string{"worker"}, "no .local name"},
		{"unknown service", []string{"nope"}, "could not find"},
	}
	for _, tc := range cases {
		_, err := qrTarget(qrServices(), tc.args, false, false)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
}
