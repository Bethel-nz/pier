//go:build e2e

// Package e2e runs the real pier binary through the core loop: pier up starts
// an app and serves it, the app answers through its .local name and its LAN
// address, and Ctrl-C, pier down, and pier clean leave nothing behind.
//
//	go test -tags e2e ./e2e/
//
// Tailscale is not needed: without it Pier serves local names only. Pier's
// state, CA, and daemon files go to a temporary config directory.
package e2e

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const body = "pier e2e ok /hello"

func TestCoreLoop(t *testing.T) {
	bin := t.TempDir()
	pier := build(t, "../cmd/pier", filepath.Join(bin, "pier"))
	server := build(t, "./testdata/server", filepath.Join(bin, "server"))
	echo := build(t, "./testdata/echo", filepath.Join(bin, "echo"))

	home := t.TempDir()
	env := isolated(home)
	project := t.TempDir()
	port, dbPort := freePort(t), freePort(t)
	stamp := time.Now().UnixNano() % 1_000_000
	name, dbName := fmt.Sprintf("e2e-%d.local", stamp), fmt.Sprintf("e2e-db-%d.local", stamp)
	writeFile(t, filepath.Join(project, "pier.yaml"), fmt.Sprintf(`version: 1
name: e2e
services:
  web:
    target: localhost:%d
    local: %s
    run: %q
  db:
    protocol: tcp
    target: localhost:%d
    local: %s
    run: %q
`, port, name, server, dbPort, dbName, echo))

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(pier, args...)
		cmd.Dir, cmd.Env = project, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("pier %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	t.Cleanup(func() { // whatever happened, stop the daemon and remove Pier's files
		for _, args := range [][]string{{"down"}, {"clean"}} {
			cmd := exec.Command(pier, args...)
			cmd.Dir, cmd.Env = project, env
			_ = cmd.Run()
		}
	})

	// pier up: starts the app, waits for it, serves it, and stays in the foreground.
	up := exec.Command(pier, "up", "--no-color")
	up.Dir, up.Env = project, env
	output := &lines{}
	up.Stdout, up.Stderr = output, output
	startGroup(up)
	if err := up.Start(); err != nil {
		t.Fatal(err)
	}
	localURL := output.waitFor(t, regexp.MustCompile(`(?m)^local\s+web\s+(https://\S+)`), 90*time.Second)
	lanURL := output.waitFor(t, regexp.MustCompile(`(?m)^lan\s+web\s+(http://\S+)`), 10*time.Second)
	t.Logf("serving %s and %s", localURL, lanURL)

	// The .local name over HTTPS, with Pier's certificate for it. The runner may
	// not resolve .local, so dial this machine and send the name as SNI and Host.
	httpsPort := portOf(t, localURL, "443")
	certNames, got := getHTTPS(t, "127.0.0.1:"+httpsPort, name, "/hello")
	if got != body {
		t.Fatalf("%s answered %q, want %q", localURL, got, body)
	}
	if !contains(certNames, name) {
		t.Fatalf("certificate names %v do not include %s", certNames, name)
	}

	// The LAN fallback, plain HTTP on this machine's LAN address: no DNS, no CA.
	if got := getHTTP(t, strings.TrimSuffix(lanURL, "/")+"/hello"); got != body {
		t.Fatalf("%s answered %q, want %q", lanURL, got, body)
	}

	// The TCP service, relayed raw from this machine's LAN address.
	tcpLAN := output.waitFor(t, regexp.MustCompile(`(?m)^lan\s+db\s+tcp://(\S+)`), 10*time.Second)
	if reply := roundTrip(t, tcpLAN, "select 1"); reply != "echo select 1" {
		t.Fatalf("TCP relay at %s answered %q", tcpLAN, reply)
	}

	// pier status sees the same thing.
	if status := run("status", "--no-color"); !strings.Contains(status, localURL) || !strings.Contains(status, lanURL) || !strings.Contains(status, "tcp://"+tcpLAN) {
		t.Fatalf("pier status does not show every address:\n%s", status)
	}

	// Ctrl-C stops the app, and everything it started.
	interrupt(t, up)
	done := make(chan error, 1)
	go func() { done <- up.Wait() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		_ = up.Process.Kill()
		t.Fatalf("pier up did not exit after Ctrl-C:\n%s", output)
	}
	waitClosed(t, fmt.Sprintf("127.0.0.1:%d", port), "the app")
	waitClosed(t, fmt.Sprintf("127.0.0.1:%d", dbPort), "the TCP service")

	// pier down withdraws the name; with no project left, the daemon exits and
	// its ports close.
	if out := run("down", "--no-color"); !strings.Contains(out, "local names withdrawn") {
		t.Fatalf("pier down said:\n%s", out)
	}
	waitClosed(t, "127.0.0.1:"+httpsPort, "the .local HTTPS port")
	waitClosed(t, hostOf(t, lanURL), "the LAN port")
	waitClosed(t, tcpLAN, "the TCP relay")

	// pier clean removes the CA and the project's certificate.
	run("clean", "--no-color")
	for _, path := range []string{filepath.Join(project, ".pier", "certs"), caDir(home)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s is still there after pier clean", path)
		}
	}
}

func build(t *testing.T, pkg, out string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, pkg)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, output)
	}
	return out
}

// isolated is this process's environment with every per-user directory Pier
// uses pointed inside home.
func isolated(home string) []string {
	env := os.Environ()
	return append(env,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"APPDATA="+filepath.Join(home, "AppData", "Roaming"),
		"NO_COLOR=1",
	)
}

func caDir(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "pier", "ca")
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "pier", "ca")
	default:
		return filepath.Join(home, ".config", "pier", "ca")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// lines collects pier up's output for matching while it runs.
type lines struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func (l *lines) waitFor(t *testing.T, pattern *regexp.Regexp, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if match := pattern.FindStringSubmatch(l.String()); match != nil {
			return match[1]
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("pier up never printed %s:\n%s", pattern, l)
	return ""
}

func portOf(t *testing.T, raw, fallback string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if port := u.Port(); port != "" {
		return port
	}
	return fallback
}

func hostOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

// getHTTPS asks addr for path as name, and returns the names on the
// certificate it presented along with the body.
func getHTTPS(t *testing.T, addr, name, path string) ([]string, string) {
	t.Helper()
	var names []string
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{
			ServerName:         name,
			InsecureSkipVerify: true, // the CA is not trusted on a CI runner; the names are checked below
			VerifyConnection: func(cs tls.ConnectionState) error {
				names = cs.PeerCertificates[0].DNSNames
				return nil
			},
		},
	}}
	resp, err := client.Get("https://" + name + path)
	if err != nil {
		t.Fatalf("GET https://%s%s via %s: %v", name, path, addr, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return names, string(data)
}

func getHTTP(t *testing.T, raw string) string {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Get(raw)
	if err != nil {
		t.Fatalf("GET %s: %v", raw, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return string(data)
}

// roundTrip sends one line to addr over TCP and returns the line it answers.
func roundTrip(t *testing.T, addr, line string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := fmt.Fprintln(conn, line); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read from %s: %v", addr, err)
	}
	return strings.TrimSpace(reply)
}

// waitClosed fails unless nothing answers at addr within a few seconds.
func waitClosed(t *testing.T, addr, what string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("%s (%s) still answers; something was left running", what, addr)
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
