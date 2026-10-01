package localname

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bethel-nz/pier/internal/state"
)

// TestMain lets this test binary stand in for cloudflared: run with
// PIER_FAKE_CLOUDFLARED set, it serves /ready on --metrics, or logs an
// error and exits when its config routes fail.example.com.
func TestMain(m *testing.M) {
	if os.Getenv("PIER_FAKE_CLOUDFLARED") != "" {
		fakeCloudflared(os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

func fakeCloudflared(args []string) {
	flags := flag.NewFlagSet("cloudflared", flag.ExitOnError)
	flags.Bool("no-autoupdate", false, "")
	config := flags.String("config", "", "")
	metrics := flags.String("metrics", "", "")
	logFile := flags.String("logfile", "", "")
	_ = flags.Parse(args[1:]) // after "tunnel"
	contents, _ := os.ReadFile(*config)
	if strings.Contains(string(contents), "fail.example.com") {
		line := `{"level":"error","message":"Couldn't start tunnel","error":"Unauthorized: Invalid tunnel secret"}`
		_ = os.WriteFile(*logFile, []byte(line+"\n"), 0o600)
		os.Exit(1)
	}
	http.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	fmt.Println(http.ListenAndServe(*metrics, nil))
	os.Exit(1)
}

func tunnelProject(t *testing.T, id, host string) state.ProjectState {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return state.ProjectState{ProjectID: id, Path: t.TempDir(), Tunnel: &state.Tunnel{
		ID: "6f1c", Name: "pier-" + id, Credentials: "/creds.json", Binary: exe,
		Hosts: []state.TunnelHost{{Service: "web", Hostname: host, Target: "http://127.0.0.1:3000"}},
	}}
}

// waitTunnel syncs until the project's tunnel leaves connecting.
func waitTunnel(t *testing.T, d *daemon, saved []state.ProjectState) TunnelStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		statuses := d.syncTunnels(saved, time.Now())
		if len(statuses) != 1 {
			t.Fatalf("statuses = %+v", statuses)
		}
		if statuses[0].State != TunnelConnecting || time.Now().After(deadline) {
			return statuses[0]
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestTunnelRunsRestartsAndStops(t *testing.T) {
	t.Setenv("PIER_FAKE_CLOUDFLARED", "1")
	d := &daemon{tunnels: map[string]*tunnelProcess{}}
	defer d.closeTunnels()
	project := tunnelProject(t, "p1", "app.example.com")
	saved := []state.ProjectState{project}

	status := waitTunnel(t, d, saved)
	if status.State != TunnelConnected || status.Name != "pier-p1" || status.Hosts[0] != "app.example.com" {
		t.Fatalf("status = %+v", status)
	}
	config, err := os.ReadFile(filepath.Join(project.Path, ".pier", "cloudflared.yml"))
	if err != nil || !strings.Contains(string(config), "hostname: app.example.com") {
		t.Fatalf("config = %s, %v", config, err)
	}
	first := d.tunnels["p1"].cmd.Process.Pid

	// A new hostname restarts cloudflared with the new config.
	project.Tunnel.Hosts = append(project.Tunnel.Hosts, state.TunnelHost{Service: "api", Hostname: "api.example.com", Target: "http://127.0.0.1:4000"})
	if status := waitTunnel(t, d, saved); status.State != TunnelConnected || len(status.Hosts) != 2 {
		t.Fatalf("after adding a host: %+v", status)
	}
	if d.tunnels["p1"].cmd.Process.Pid == first {
		t.Fatal("cloudflared was not restarted for the new config")
	}

	// Nothing left to serve: cloudflared stops.
	proc := d.tunnels["p1"]
	if statuses := d.syncTunnels(nil, time.Now()); len(statuses) != 0 || len(d.tunnels) != 0 {
		t.Fatalf("statuses = %+v, tunnels = %v", statuses, d.tunnels)
	}
	select {
	case <-proc.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("cloudflared still runs after its project stopped serving")
	}
}

func TestTunnelFailureIsReportedAndRetried(t *testing.T) {
	t.Setenv("PIER_FAKE_CLOUDFLARED", "1")
	d := &daemon{tunnels: map[string]*tunnelProcess{}}
	defer d.closeTunnels()
	saved := []state.ProjectState{tunnelProject(t, "p2", "fail.example.com")}

	status := waitTunnel(t, d, saved)
	if status.State != TunnelFailed || !strings.Contains(status.Detail, "Invalid tunnel secret") {
		t.Fatalf("status = %+v", status)
	}
	proc := d.tunnels["p2"]
	if proc.failures != 1 || !proc.retryAt.After(time.Now()) {
		t.Fatalf("failures=%d retryAt=%v; want one failure and a retry later", proc.failures, proc.retryAt)
	}
	// Before the backoff ends, it is not started again.
	d.syncTunnels(saved, time.Now())
	if proc.cmd != nil {
		t.Fatal("cloudflared restarted before its backoff ended")
	}
	// After it, it is.
	d.syncTunnels(saved, proc.retryAt)
	if proc.cmd == nil {
		t.Fatal("cloudflared was not restarted after its backoff")
	}
}
