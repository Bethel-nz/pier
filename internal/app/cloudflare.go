package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Bethel-nz/pier/internal/cloudflare"
	"github.com/Bethel-nz/pier/internal/localname"
	"github.com/Bethel-nz/pier/internal/state"
)

// Tunnels sets up Cloudflare Tunnels through cloudflared.
type Tunnels interface {
	// Binary is cloudflared's absolute path, or cloudflare.ErrNotInstalled.
	Binary() (string, error)
	LoggedIn() bool
	// Login opens the browser to authorize a Cloudflare zone and waits.
	Login(ctx context.Context) error
	// EnsureTunnel finds or creates the tunnel called name, with its credentials.
	EnsureTunnel(ctx context.Context, name string) (cloudflare.Tunnel, bool, error)
	RouteDNS(ctx context.Context, tunnelID, hostname string, overwrite bool) error
}

// TunnelSetup is what pier up changed in Cloudflare.
type TunnelSetup struct {
	// Tunnel is the project's tunnel name; empty when it has no hostname.
	Tunnel   string
	LoggedIn bool // this run logged in to Cloudflare
	Created  bool // this run created the tunnel
	// Routed are hostnames this run pointed at the tunnel.
	Routed []string
}

// EnableCloudflare lets pier up log in to Cloudflare and create tunnels.
// interactive reports whether a person is at the terminal for the browser
// login; otherwise it is left to them.
func (s *Service) EnableCloudflare(interactive func() bool) {
	s.tunnels = &liveTunnels{interactive: interactive}
}

// setupTunnel makes sure the project's tunnel exists and every hostname
// routes to it, then returns the tunnel the daemon should run. Paused
// services are left out; a hostname already routed is not routed again.
func (s *Service) setupTunnel(ctx context.Context, sess *session, paused map[string]bool, force bool) (*state.Tunnel, TunnelSetup, error) {
	var setup TunnelSetup
	if !sess.cfg.HasCloudflare() {
		return nil, setup, nil
	}
	if s.tunnels == nil {
		return nil, setup, errors.New("Pier cannot serve cloudflare: hostnames from here; run pier up")
	}
	binary, err := s.tunnels.Binary()
	if err != nil {
		return nil, setup, &PrerequisiteError{Err: err}
	}
	name := sess.cfg.TunnelName()
	setup.Tunnel = name
	saved := sess.state.Tunnel
	routed := map[string]bool{}
	if saved != nil && saved.Name == name {
		for _, host := range saved.Hosts {
			routed[host.Hostname] = true
		}
	}

	tunnel := &state.Tunnel{Name: name, Binary: binary}
	if saved != nil && saved.Name == name && fileExists(saved.Credentials) {
		tunnel.ID, tunnel.Credentials = saved.ID, saved.Credentials
	}
	targets := map[string]string{}
	for _, tap := range sess.taps {
		targets[tap.Service] = tap.URL() // throttled or captured like its other URLs
	}
	for _, service := range sess.cfg.Services {
		if service.Cloudflare == "" || paused[service.Name] {
			continue
		}
		target := targets[service.Name]
		if target == "" {
			target = service.Target
		}
		tunnel.Hosts = append(tunnel.Hosts, state.TunnelHost{Service: service.Name, Hostname: service.Cloudflare, Target: target})
	}

	needsCloudflare := tunnel.ID == ""
	for _, host := range tunnel.Hosts {
		needsCloudflare = needsCloudflare || !routed[host.Hostname]
	}
	if !needsCloudflare {
		return tunnel, setup, nil
	}
	if !s.tunnels.LoggedIn() {
		if err := s.tunnels.Login(ctx); err != nil {
			return nil, setup, err
		}
		setup.LoggedIn = true
	}
	if tunnel.ID == "" {
		made, created, err := s.tunnels.EnsureTunnel(ctx, name)
		if err != nil {
			return nil, setup, err
		}
		tunnel.ID, tunnel.Credentials, setup.Created = made.ID, made.Credentials, created
		routed = map[string]bool{} // a different tunnel: its records point elsewhere
	}
	for _, host := range tunnel.Hosts {
		if routed[host.Hostname] {
			continue
		}
		if err := s.tunnels.RouteDNS(ctx, tunnel.ID, host.Hostname, force); err != nil {
			return nil, setup, err
		}
		setup.Routed = append(setup.Routed, host.Hostname)
	}
	return tunnel, setup, nil
}

// cloudflareWarnings explains what stops the project's cloudflare: hostnames
// from being served, for pier doctor.
func (s *Service) cloudflareWarnings(projectID string) []string {
	if s.tunnels == nil {
		return nil
	}
	if _, err := s.tunnels.Binary(); err != nil {
		return []string{"cloudflare: " + err.Error()}
	}
	if !s.tunnels.LoggedIn() {
		return []string{"cloudflare: not logged in; pier up opens the browser to log in, or run cloudflared tunnel login"}
	}
	if s.locals == nil {
		return nil
	}
	if status, running := s.locals.Status().Tunnel(projectID); running && status.State == localname.TunnelFailed {
		return []string{"cloudflare: tunnel " + status.Name + " failed: " + status.Detail}
	}
	return nil
}

// withTunnel fills each service's Cloudflare URL and state from the daemon.
func withTunnel(infos []ServiceInfo, report localname.Report, projectID string) {
	status, running := report.Tunnel(projectID)
	for i := range infos {
		if infos[i].Cloudflare == "" {
			continue
		}
		switch {
		case infos[i].Paused:
			infos[i].CloudflareState = "paused"
		case !running:
			infos[i].CloudflareState = "down"
		default:
			infos[i].CloudflareState, infos[i].CloudflareDetail = status.State, status.Detail
			if status.State == localname.TunnelConnected {
				infos[i].CloudflareURL = "https://" + infos[i].Cloudflare + "/"
			}
		}
	}
}

// liveTunnels finds cloudflared the first time it is needed.
type liveTunnels struct {
	interactive func() bool
	once        sync.Once
	client      *cloudflare.Client
	err         error
}

func (l *liveTunnels) find() (*cloudflare.Client, error) {
	l.once.Do(func() { l.client, l.err = cloudflare.Find(nil) })
	return l.client, l.err
}

func (l *liveTunnels) Binary() (string, error) {
	client, err := l.find()
	if err != nil {
		return "", err
	}
	return client.Binary, nil
}

func (l *liveTunnels) LoggedIn() bool { return cloudflare.OriginCert() != "" }

func (l *liveTunnels) Login(ctx context.Context) error {
	client, err := l.find()
	if err != nil {
		return err
	}
	if l.interactive == nil || !l.interactive() {
		return errors.New("Pier is not logged in to Cloudflare; run cloudflared tunnel login, then pier up")
	}
	fmt.Fprintln(os.Stderr, "cloudflare   not logged in; opening your browser to pick a domain…")
	return client.Login(ctx)
}

func (l *liveTunnels) EnsureTunnel(ctx context.Context, name string) (cloudflare.Tunnel, bool, error) {
	client, err := l.find()
	if err != nil {
		return cloudflare.Tunnel{}, false, err
	}
	dir, err := credentialsDir()
	if err != nil {
		return cloudflare.Tunnel{}, false, err
	}
	return client.EnsureTunnel(ctx, name, dir)
}

func (l *liveTunnels) RouteDNS(ctx context.Context, tunnelID, hostname string, overwrite bool) error {
	client, err := l.find()
	if err != nil {
		return err
	}
	return client.RouteDNS(ctx, tunnelID, hostname, overwrite)
}

// credentialsDir keeps tunnel secrets with Pier's other per-user files.
func credentialsDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "pier", "cloudflare"), nil
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// idleTunnel keeps a tunnel's identity with nothing to serve, so the next
// pier up reuses it without asking Cloudflare.
func idleTunnel(tunnel *state.Tunnel) *state.Tunnel {
	if tunnel == nil {
		return nil
	}
	idle := *tunnel
	idle.Hosts = nil
	return &idle
}
