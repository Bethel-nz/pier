package tailscale

import (
	"reflect"
	"strings"
	"testing"
)

func TestUpArgs(t *testing.T) {
	tests := []struct {
		name  string
		route Route
		want  []string
	}{
		{
			name:  "public route uses funnel on 443",
			route: Route{HTTPSPort: 443, Path: "/api", Target: "http://127.0.0.1:4000", Public: true},
			want:  []string{"funnel", "--bg", "--yes", "--https=443", "--set-path=/api", "http://127.0.0.1:4000"},
		},
		{
			name:  "tailnet route uses serve on 8443",
			route: Route{HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000", Public: false},
			want:  []string{"serve", "--bg", "--yes", "--https=8443", "--set-path=/", "http://127.0.0.1:3000"},
		},
		{
			name:  "public route on 8443 is an allowed Funnel listener",
			route: Route{HTTPSPort: 8443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true},
			want:  []string{"funnel", "--bg", "--yes", "--https=8443", "--set-path=/hooks", "http://127.0.0.1:8787"},
		},
		{
			name:  "public route on 10000 is an allowed Funnel listener",
			route: Route{HTTPSPort: 10000, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true},
			want:  []string{"funnel", "--bg", "--yes", "--https=10000", "--set-path=/hooks", "http://127.0.0.1:8787"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UpArgs(tt.route)
			if err != nil {
				t.Fatalf("UpArgs() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("UpArgs() = %#v, want %#v", got, tt.want)
			}
			assertNoReset(t, got)
		})
	}
}

func TestUpArgsRejectsInvalidFunnelPort(t *testing.T) {
	_, err := UpArgs(Route{HTTPSPort: 80, Path: "/", Target: "http://127.0.0.1:3000", Public: true})
	if err == nil {
		t.Fatal("UpArgs() error = nil, want invalid Funnel port")
	}
	if !strings.Contains(err.Error(), "443") || !strings.Contains(err.Error(), "8443") || !strings.Contains(err.Error(), "10000") {
		t.Errorf("UpArgs() error = %q, want a Pier explanation of allowed Funnel ports", err)
	}
}

func TestDownArgs(t *testing.T) {
	tests := []struct {
		name  string
		route Route
		want  []string
	}{
		{
			name:  "public route removes a path-specific funnel handler",
			route: Route{HTTPSPort: 443, Path: "/api", Target: "http://127.0.0.1:4000", Public: true},
			want:  []string{"funnel", "--https=443", "--set-path=/api", "off"},
		},
		{
			name:  "tailnet route removes a path-specific serve handler",
			route: Route{HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000", Public: false},
			want:  []string{"serve", "--https=8443", "--set-path=/", "off"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DownArgs(tt.route)
			if err != nil {
				t.Fatalf("DownArgs() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DownArgs() = %#v, want %#v", got, tt.want)
			}
			assertNoReset(t, got)
		})
	}
}

func TestDownArgsRejectsInvalidFunnelPort(t *testing.T) {
	_, err := DownArgs(Route{HTTPSPort: 8080, Path: "/api", Target: "http://127.0.0.1:4000", Public: true})
	if err == nil {
		t.Fatal("DownArgs() error = nil, want invalid Funnel port")
	}
}

func assertNoReset(t *testing.T, args []string) {
	t.Helper()
	for _, arg := range args {
		if arg == "reset" || strings.Contains(arg, "reset") {
			t.Errorf("generated args %#v contain reset", args)
		}
	}
}
