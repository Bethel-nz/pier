package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Bethel-nz/pier/internal/app"
)

func TestUpSaysCloudflareCanReadNewHostnames(t *testing.T) {
	const note = "Cloudflare ends TLS for these hostnames, so it can read their traffic"
	up := func(setup app.TunnelSetup) string {
		var out bytes.Buffer
		if err := (Options{Out: &out, Err: &out}).Up(app.UpResult{Cloudflare: setup}, nil); err != nil {
			t.Fatalf("Up() error = %v", err)
		}
		return out.String()
	}
	if got := up(app.TunnelSetup{Tunnel: "pier-myapp", Routed: []string{"web.example.com"}}); !strings.Contains(got, note) {
		t.Errorf("Up() after routing a hostname = %q, want the Cloudflare note", got)
	}
	if got := up(app.TunnelSetup{Tunnel: "pier-myapp"}); strings.Contains(got, note) {
		t.Errorf("Up() with nothing new routed = %q, want no note", got)
	}
}
