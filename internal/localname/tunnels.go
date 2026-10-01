package localname

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Bethel-nz/pier/internal/cloudflare"
	"github.com/Bethel-nz/pier/internal/state"
)

// Tunnel states reported by the daemon.
const (
	TunnelConnecting = "connecting"
	TunnelConnected  = "connected"
	TunnelFailed     = "failed"
)

// TunnelStatus is one project's Cloudflare Tunnel as the daemon last saw it.
type TunnelStatus struct {
	Project string   `json:"project"`
	Name    string   `json:"name"`
	Hosts   []string `json:"hosts"`
	State   string   `json:"state"`
	Detail  string   `json:"detail,omitempty"`
}

// tunnelProcess is one running cloudflared. It is restarted, never
// reconfigured: cloudflared reads its config only at start.
type tunnelProcess struct {
	binary  string
	config  []byte
	logFile string
	metrics string
	cmd     *exec.Cmd
	exited  chan struct{}
	exitErr error
	// failures counts exits in a row, for the backoff before the next start.
	failures int
	retryAt  time.Time
	detail   string
}

// syncTunnels runs cloudflared for every project whose tunnel serves a
// hostname, restarting it when its config changes or it exits.
func (d *daemon) syncTunnels(saved []state.ProjectState, now time.Time) []TunnelStatus {
	var statuses []TunnelStatus
	wanted := map[string]bool{}
	for _, project := range saved {
		tunnel := project.Tunnel
		if project.Path == "" || !tunnel.Serving() {
			continue
		}
		wanted[project.ProjectID] = true
		status := TunnelStatus{Project: project.ProjectID, Name: tunnel.Name}
		routes := make([]cloudflare.Route, 0, len(tunnel.Hosts))
		for _, host := range tunnel.Hosts {
			status.Hosts = append(status.Hosts, host.Hostname)
			routes = append(routes, cloudflare.Route{Hostname: host.Hostname, Target: host.Target})
		}
		config, err := cloudflare.Config(cloudflare.Tunnel{ID: tunnel.ID, Name: tunnel.Name, Credentials: tunnel.Credentials}, routes)
		if err != nil {
			status.State, status.Detail = TunnelFailed, err.Error()
			statuses = append(statuses, status)
			continue
		}
		proc := d.tunnels[project.ProjectID]
		if proc != nil && (proc.binary != tunnel.Binary || !bytes.Equal(proc.config, config)) {
			proc.stop()
			proc = nil
		}
		if proc == nil {
			proc = &tunnelProcess{binary: tunnel.Binary, config: config}
			d.tunnels[project.ProjectID] = proc
			proc.start(filepath.Join(project.Path, ".pier"))
		} else if proc.exitedNow() && !now.Before(proc.retryAt) {
			proc.start(filepath.Join(project.Path, ".pier"))
		}
		status.State, status.Detail = proc.state()
		statuses = append(statuses, status)
	}
	for id, proc := range d.tunnels {
		if !wanted[id] {
			proc.stop()
			delete(d.tunnels, id)
		}
	}
	return statuses
}

// start writes the config next to the project and launches cloudflared.
func (p *tunnelProcess) start(dir string) {
	p.cmd, p.exitErr = nil, nil
	fail := func(err error) {
		p.detail = err.Error()
		p.backoff()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fail(err)
		return
	}
	configPath := filepath.Join(dir, "cloudflared.yml")
	if err := os.WriteFile(configPath, p.config, 0o600); err != nil {
		fail(err)
		return
	}
	port, err := freeLoopbackPort()
	if err != nil {
		fail(err)
		return
	}
	p.metrics = "127.0.0.1:" + strconv.Itoa(port)
	p.logFile = filepath.Join(dir, "cloudflared.log")
	_ = os.Remove(p.logFile) // cloudflared appends; keep only this run
	cmd := exec.Command(p.binary, cloudflare.RunArgs(configPath, p.metrics, p.logFile)...)
	if err := cmd.Start(); err != nil {
		fail(fmt.Errorf("Pier could not start cloudflared: %w", err))
		return
	}
	p.cmd = cmd
	p.exited = make(chan struct{})
	go func(exited chan struct{}) {
		p.exitErr = cmd.Wait()
		close(exited)
	}(p.exited)
}

// exitedNow reports whether cloudflared is not running, recording why and
// when to try again the first time it notices.
func (p *tunnelProcess) exitedNow() bool {
	if p.cmd == nil {
		return true
	}
	select {
	case <-p.exited:
	default:
		return false
	}
	p.detail = lastLogError(p.logFile)
	if p.detail == "" {
		p.detail = fmt.Sprintf("cloudflared exited (%v)", p.exitErr)
	}
	p.cmd = nil
	p.backoff()
	return true
}

// backoff waits 2s, 4s, 8s, … up to a minute before the next start.
func (p *tunnelProcess) backoff() {
	p.failures++
	wait := time.Duration(1<<min(p.failures, 6)) * time.Second
	p.retryAt = time.Now().Add(min(wait, time.Minute))
}

func (p *tunnelProcess) state() (string, string) {
	if p.exitedNow() {
		return TunnelFailed, p.detail
	}
	if ready(p.metrics) {
		p.failures = 0
		return TunnelConnected, ""
	}
	return TunnelConnecting, ""
}

func (p *tunnelProcess) stop() {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Kill()
	select {
	case <-p.exited:
	case <-time.After(2 * time.Second):
	}
	p.cmd = nil
}

func (d *daemon) closeTunnels() {
	for id, proc := range d.tunnels {
		proc.stop()
		delete(d.tunnels, id)
	}
}

// ready asks cloudflared's metrics server whether the tunnel has a live
// connection to Cloudflare.
func ready(metrics string) bool {
	client := http.Client{Timeout: 300 * time.Millisecond}
	resp, err := client.Get("http://" + metrics + "/ready")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// lastLogError is the last error cloudflared logged, from its JSON log file.
func lastLogError(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	last := ""
	lines := bufio.NewScanner(file)
	lines.Buffer(make([]byte, 64*1024), 1024*1024)
	for lines.Scan() {
		var entry struct {
			Level   string `json:"level"`
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		if json.Unmarshal(lines.Bytes(), &entry) != nil || (entry.Level != "error" && entry.Level != "fatal") {
			continue
		}
		last = entry.Message
		if entry.Error != "" {
			last += ": " + entry.Error
		}
	}
	return last
}

func freeLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
