//go:build !windows

package localname

import (
	"os/exec"
	"testing"
	"time"
)

func startForStop(t *testing.T, script string) *tunnelProcess {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &tunnelProcess{cmd: cmd, exited: make(chan struct{})}
	go func() {
		p.exitErr = cmd.Wait()
		close(p.exited)
	}()
	time.Sleep(100 * time.Millisecond) // let sh set its trap
	return p
}

func TestTunnelStopAsksCloudflaredToExitFirst(t *testing.T) {
	grace := tunnelStopGrace
	tunnelStopGrace = 5 * time.Second
	t.Cleanup(func() { tunnelStopGrace = grace })

	// Exits cleanly on SIGTERM, like cloudflared unregistering its connections.
	p := startForStop(t, `trap 'exit 0' TERM; while :; do sleep 0.05; done`)
	started := time.Now()
	p.stop()
	if p.exitErr != nil || time.Since(started) > 2*time.Second {
		t.Fatalf("stop took %s, exit %v; want a clean exit on SIGTERM", time.Since(started), p.exitErr)
	}
}

func TestTunnelStopKillsAProcessThatIgnoresSIGTERM(t *testing.T) {
	grace := tunnelStopGrace
	tunnelStopGrace = 200 * time.Millisecond
	t.Cleanup(func() { tunnelStopGrace = grace })

	p := startForStop(t, `trap '' TERM; while :; do sleep 0.05; done`)
	started := time.Now()
	p.stop()
	select {
	case <-p.exited:
	default:
		t.Fatal("the process is still running after stop")
	}
	if elapsed := time.Since(started); elapsed < tunnelStopGrace {
		t.Fatalf("killed after %s, before the %s grace", elapsed, tunnelStopGrace)
	}
}
