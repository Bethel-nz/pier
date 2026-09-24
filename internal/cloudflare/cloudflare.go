// Package cloudflare is Pier's process boundary for cloudflared: it logs in,
// creates a project's tunnel, routes hostnames to it, and writes the config
// the daemon runs cloudflared with.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner executes cloudflared and captures its output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// ExecRunner runs commands with the operating system process API.
type ExecRunner struct{}

// Run executes one command and captures stdout and stderr independently.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// ErrNotInstalled means cloudflared is not on PATH.
var ErrNotInstalled = errors.New("cloudflared is not installed; get it from https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/ and run pier up again")

// ErrRecordExists means a hostname already has a DNS record that Pier did not make.
var ErrRecordExists = errors.New("already has a DNS record")

// Tunnel is a named Cloudflare Tunnel and the file holding its secret.
type Tunnel struct {
	ID          string
	Name        string
	Credentials string
}

// Client runs cloudflared.
type Client struct {
	// Binary is the absolute path to cloudflared.
	Binary string
	runner Runner
	// login runs `cloudflared tunnel login` attached to the terminal.
	login func(ctx context.Context, binary string) error
}

// Find locates cloudflared. A nil runner selects the real process runner.
func Find(runner Runner) (*Client, error) {
	binary, err := exec.LookPath("cloudflared")
	if err != nil {
		return nil, ErrNotInstalled
	}
	if abs, err := filepath.Abs(binary); err == nil {
		binary = abs
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Client{Binary: binary, runner: runner, login: interactiveLogin}, nil
}

// LoggedIn reports whether cloudflared has an origin certificate, which
// `cloudflared tunnel login` writes and every tunnel command needs.
func (c *Client) LoggedIn() bool {
	return OriginCert() != ""
}

// Login opens the browser for the user to authorize a zone. It waits until
// they do, printing cloudflared's instructions to the terminal.
func (c *Client) Login(ctx context.Context) error {
	if err := c.login(ctx, c.Binary); err != nil {
		return fmt.Errorf("cloudflared login did not finish: %w", err)
	}
	if !c.LoggedIn() {
		return errors.New("cloudflared login finished without a certificate; run cloudflared tunnel login")
	}
	return nil
}

func interactiveLogin(ctx context.Context, binary string) error {
	command := exec.CommandContext(ctx, binary, "tunnel", "login")
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stderr, os.Stderr
	return command.Run()
}

// OriginCert is the path of cloudflared's origin certificate, or "" when
// there is none. It looks where cloudflared does.
func OriginCert() string {
	candidates := []string{os.Getenv("TUNNEL_ORIGIN_CERT")}
	if home, err := os.UserHomeDir(); err == nil {
		for _, dir := range []string{".cloudflared", ".cloudflare-warp", "cloudflare-warp"} {
			candidates = append(candidates, filepath.Join(home, dir, "cert.pem"))
		}
	}
	candidates = append(candidates, "/etc/cloudflared/cert.pem", "/usr/local/etc/cloudflared/cert.pem")
	for _, path := range candidates {
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

// EnsureTunnel finds the tunnel called name, creating it if needed, and makes
// sure its credentials are in dir. created reports a new tunnel.
func (c *Client) EnsureTunnel(ctx context.Context, name, dir string) (tunnel Tunnel, created bool, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Tunnel{}, false, err
	}
	existing, err := c.find(ctx, name)
	if err != nil {
		return Tunnel{}, false, err
	}
	if existing.ID != "" {
		existing.Credentials = filepath.Join(dir, existing.ID+".json")
		if _, statErr := os.Stat(existing.Credentials); statErr == nil {
			return existing, false, nil
		}
		// Made on another machine, or its file was deleted: fetch the secret.
		if _, err := c.run(ctx, "tunnel", "token", "--cred-file", existing.Credentials, existing.ID); err != nil {
			return Tunnel{}, false, fmt.Errorf("Pier could not fetch the credentials of tunnel %s: %w", name, err)
		}
		return existing, false, nil
	}

	// cloudflared names the file after the ID it has not chosen yet, so
	// create into a temporary file and move it.
	pending := filepath.Join(dir, name+".pending.json")
	out, err := c.run(ctx, "tunnel", "create", "--output", "json", "--cred-file", pending, name)
	if err != nil {
		return Tunnel{}, false, fmt.Errorf("Pier could not create tunnel %s: %w", name, err)
	}
	var made struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &made); err != nil || made.ID == "" {
		return Tunnel{}, false, fmt.Errorf("Pier could not read the tunnel cloudflared created: %s", strings.TrimSpace(string(out)))
	}
	tunnel = Tunnel{ID: made.ID, Name: name, Credentials: filepath.Join(dir, made.ID+".json")}
	if err := os.Rename(pending, tunnel.Credentials); err != nil {
		return Tunnel{}, false, err
	}
	return tunnel, true, nil
}

func (c *Client) find(ctx context.Context, name string) (Tunnel, error) {
	out, err := c.run(ctx, "tunnel", "list", "--output", "json", "--name", name)
	if err != nil {
		return Tunnel{}, fmt.Errorf("Pier could not list Cloudflare tunnels: %w", err)
	}
	var tunnels []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if trimmed := bytes.TrimSpace(out); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		if err := json.Unmarshal(trimmed, &tunnels); err != nil {
			return Tunnel{}, fmt.Errorf("Pier could not read cloudflared's tunnel list: %w", err)
		}
	}
	for _, tunnel := range tunnels {
		if tunnel.Name == name {
			return Tunnel{ID: tunnel.ID, Name: tunnel.Name}, nil
		}
	}
	return Tunnel{}, nil
}

// RouteDNS points hostname at the tunnel with a CNAME record. A hostname
// already routed to this tunnel is left alone. A record pointing anywhere else
// is replaced only when overwrite is set; otherwise the error wraps
// ErrRecordExists.
func (c *Client) RouteDNS(ctx context.Context, tunnelID, hostname string, overwrite bool) error {
	args := []string{"tunnel", "route", "dns"}
	if overwrite {
		args = append(args, "--overwrite-dns")
	}
	_, err := c.run(ctx, append(args, tunnelID, hostname)...)
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("%s %w; run pier up --force to point it at this project's tunnel", hostname, ErrRecordExists)
	}
	return fmt.Errorf("Pier could not route %s to its tunnel: %w", hostname, err)
}

// run executes cloudflared, turning a failure into its last line of output.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	stdout, stderr, err := c.runner.Run(ctx, c.Binary, args...)
	if err != nil {
		return stdout, errors.New(lastLine(stderr, stdout, err))
	}
	return stdout, nil
}

// lastLine is the most useful line cloudflared printed: the last one, where
// it explains a failure.
func lastLine(stderr, stdout []byte, err error) string {
	for _, output := range [][]byte{stderr, stdout} {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		if line := strings.TrimSpace(lines[len(lines)-1]); line != "" {
			return line
		}
	}
	return err.Error()
}
