package cloudflare

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner answers cloudflared commands by their subcommand, and writes
// the credentials file cloudflared would.
type fakeRunner struct {
	calls   []string
	answers map[string]answer
}

type answer struct {
	stdout, stderr string
	fail           bool
}

func (f *fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, []byte, error) {
	call := strings.Join(args, " ")
	f.calls = append(f.calls, call)
	for i, arg := range args {
		if arg == "--cred-file" {
			_ = os.WriteFile(args[i+1], []byte(`{"secret":"s"}`), 0o600)
		}
	}
	for prefix, a := range f.answers {
		if strings.HasPrefix(call, prefix) {
			if a.fail {
				return []byte(a.stdout), []byte(a.stderr), errors.New("exit status 1")
			}
			return []byte(a.stdout), []byte(a.stderr), nil
		}
	}
	return nil, nil, nil
}

func TestEnsureTunnelCreatesOnce(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{answers: map[string]answer{
		"tunnel list":   {stdout: "[]"},
		"tunnel create": {stdout: `{"id":"6f1c","name":"pier-app","token":"x"}`},
	}}
	client := &Client{Binary: "cloudflared", runner: runner}
	tunnel, created, err := client.EnsureTunnel(context.Background(), "pier-app", dir)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if tunnel.ID != "6f1c" || tunnel.Credentials != filepath.Join(dir, "6f1c.json") {
		t.Fatalf("tunnel = %+v", tunnel)
	}
	if _, err := os.Stat(tunnel.Credentials); err != nil {
		t.Fatalf("credentials were not moved into place: %v", err)
	}

	// The next pier up finds it, with its credentials already here.
	runner.answers["tunnel list"] = answer{stdout: `[{"id":"6f1c","name":"pier-app"}]`}
	runner.calls = nil
	again, created, err := client.EnsureTunnel(context.Background(), "pier-app", dir)
	if err != nil || created || again != tunnel {
		t.Fatalf("again = %+v created=%v err=%v", again, created, err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %v, want only the list", runner.calls)
	}
}

func TestEnsureTunnelFetchesMissingCredentials(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{answers: map[string]answer{"tunnel list": {stdout: `[{"id":"ab12","name":"pier-app"}]`}}}
	client := &Client{Binary: "cloudflared", runner: runner}
	tunnel, created, err := client.EnsureTunnel(context.Background(), "pier-app", dir)
	if err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	want := "tunnel token --cred-file " + filepath.Join(dir, "ab12.json") + " ab12"
	if runner.calls[len(runner.calls)-1] != want {
		t.Fatalf("calls = %v, want %q last", runner.calls, want)
	}
	if _, err := os.Stat(tunnel.Credentials); err != nil {
		t.Fatal(err)
	}
}

func TestRouteDNS(t *testing.T) {
	runner := &fakeRunner{answers: map[string]answer{
		"tunnel route dns 6f1c taken.example.com": {fail: true, stderr: "2026-09-24T21:00:00Z ERR Failed to add route: code: 1003, reason: An A, AAAA, or CNAME record with that host already exists."},
	}}
	client := &Client{Binary: "cloudflared", runner: runner}
	if err := client.RouteDNS(context.Background(), "6f1c", "app.example.com", false); err != nil {
		t.Fatal(err)
	}
	err := client.RouteDNS(context.Background(), "6f1c", "taken.example.com", false)
	if !errors.Is(err, ErrRecordExists) || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v", err)
	}
	_ = client.RouteDNS(context.Background(), "6f1c", "taken.example.com", true)
	if last := runner.calls[len(runner.calls)-1]; last != "tunnel route dns --overwrite-dns 6f1c taken.example.com" {
		t.Fatalf("overwrite ran %q", last)
	}
}

func TestConfig(t *testing.T) {
	contents, err := Config(Tunnel{ID: "6f1c", Credentials: "/home/ren/creds.json"}, []Route{
		{Hostname: "app.example.com", Target: "http://127.0.0.1:3000"},
		{Hostname: "secure.example.com", Target: "https://127.0.0.1:8443"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `tunnel: 6f1c
credentials-file: /home/ren/creds.json
ingress:
    - hostname: app.example.com
      service: http://127.0.0.1:3000
    - hostname: secure.example.com
      service: https://127.0.0.1:8443
      originRequest:
        noTLSVerify: true
    - service: http_status:404
`
	if string(contents) != want {
		t.Fatalf("config:\n%s\nwant:\n%s", contents, want)
	}

	// When cloudflared is installed, it must accept the file too.
	binary, err := exec.LookPath("cloudflared")
	if err != nil {
		return
	}
	path := filepath.Join(t.TempDir(), "cloudflared.yml")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(binary, "tunnel", "--config", path, "ingress", "validate").CombinedOutput()
	if err != nil {
		t.Fatalf("cloudflared rejected the config: %v\n%s", err, out)
	}
}
