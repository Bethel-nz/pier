package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/Bethel-nz/pier/internal/cloudflare"
	"github.com/Bethel-nz/pier/internal/config"
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
	// Rechecked is set when this run asked Cloudflare again whether the
	// tunnel and its DNS records exist, rather than trusting saved state.
	Rechecked bool
	// Routed are hostnames this run pointed at the tunnel.
	Routed []string
	// Orphans are hostnames that left pier.yaml; their DNS records still
	// point at the tunnel until removed in the Cloudflare dashboard.
	Orphans []string
}

// EnableCloudflare lets pier up log in to Cloudflare and create tunnels.
// interactive reports whether a person is at the terminal for the browser
// login; otherwise it is left to them.
func (s *Service) EnableCloudflare(interactive func() bool) {
	s.tunnels = &liveTunnels{interactive: interactive}
}

// setupTunnel makes sure the project's tunnel exists and every hostname
// routes to it, then returns the tunnel the daemon should run. Paused
// services are left out; a hostname already routed is not routed again,
// unless recheck is set or the daemon says the tunnel failed.
func (s *Service) setupTunnel(ctx context.Context, sess *session, paused map[string]bool, force, recheck bool) (*state.Tunnel, TunnelSetup, error) {
	var setup TunnelSetup
	saved := sess.state.Tunnel
	if !sess.cfg.HasCloudflare() {
		if saved == nil || len(saved.Routed)+len(saved.Hosts)+len(saved.Orphans) == 0 {
			return nil, setup, nil
		}
		// Every hostname left pier.yaml: keep the tunnel's identity, and its
		// DNS records as orphans until they are removed.
		idle := idleTunnel(saved)
		idle.Routed = history(saved, nil)
		idle.Orphans = orphans(idle.Routed, sess.cfg)
		s.forgetDeleted(ctx, idle)
		setup.Tunnel, setup.Orphans = idle.Name, idle.Orphans
		return idle, setup, nil
	}
	if s.tunnels == nil {
		return nil, setup, errors.New("Pier cannot serve provider: cloudflare services from here; run pier up")
	}
	binary, err := s.tunnels.Binary()
	if err != nil {
		return nil, setup, &PrerequisiteError{Err: err}
	}
	name := sess.cfg.TunnelName(sess.project.ID)
	if saved != nil && saved.Name == sess.cfg.LegacyTunnelName() && fileExists(saved.Credentials) {
		// Made before tunnel names carried the project ID. Keep it rather
		// than move every hostname to a new tunnel.
		name = saved.Name
	}
	setup.Tunnel = name
	if !recheck && s.tunnelFailed(sess.project.ID) {
		recheck = true // the tunnel or its credentials may be gone
	}
	setup.Rechecked = recheck
	routed := map[string]bool{}
	if saved != nil && saved.Name == name && !recheck {
		for _, host := range saved.Hosts {
			routed[host.Hostname] = true
		}
	}

	tunnel := &state.Tunnel{Name: name, Binary: binary}
	if saved != nil && saved.Name == name && fileExists(saved.Credentials) && !recheck {
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

	tunnel.Routed = history(saved, tunnel.Hosts)
	tunnel.Orphans = orphans(tunnel.Routed, sess.cfg)
	s.forgetDeleted(ctx, tunnel)
	setup.Orphans = tunnel.Orphans

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

// tunnelFailed reports whether the daemon says the project's tunnel failed.
func (s *Service) tunnelFailed(projectID string) bool {
	if s.locals == nil {
		return false
	}
	status, running := s.locals.Status().Tunnel(projectID)
	return running && status.State == localname.TunnelFailed
}

// history is every hostname routed for the project: what was saved, and the
// hostnames it routes now.
func history(saved *state.Tunnel, hosts []state.TunnelHost) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if saved != nil {
		for _, name := range saved.Routed {
			add(name)
		}
		for _, host := range saved.Hosts {
			add(host.Hostname)
		}
	}
	for _, host := range hosts {
		add(host.Hostname)
	}
	sort.Strings(out)
	return out
}

// forgetDeleted drops orphans whose DNS record is gone: once deleted in the
// dashboard, a hostname no longer resolves. cloudflared cannot list records,
// so public DNS is the only way to tell.
func (s *Service) forgetDeleted(ctx context.Context, tunnel *state.Tunnel) {
	gone := s.hostGone
	if gone == nil {
		gone = hostGone
	}
	var kept []string
	deleted := map[string]bool{}
	for _, name := range tunnel.Orphans {
		if gone(ctx, name) {
			deleted[name] = true
		} else {
			kept = append(kept, name)
		}
	}
	if len(deleted) == 0 {
		return
	}
	tunnel.Orphans = kept
	tunnel.Routed = slices.DeleteFunc(tunnel.Routed, func(name string) bool { return deleted[name] })
}

// hostGone asks the system resolver, briefly, whether host exists. Only a
// definite "no such host" counts; a timeout keeps the orphan.
func hostGone(ctx context.Context, host string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := net.DefaultResolver.LookupHost(ctx, host)
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

// orphans are routed hostnames no service in cfg uses any more. A paused
// service still uses its hostname.
func orphans(routed []string, cfg config.Project) []string {
	inUse := map[string]bool{}
	for _, service := range cfg.Services {
		inUse[service.Cloudflare] = true
	}
	var out []string
	for _, name := range routed {
		if !inUse[name] {
			out = append(out, name)
		}
	}
	return out
}

// sameHistory reports whether two tunnels remember the same hostnames.
func sameHistory(a, b *state.Tunnel) bool {
	if a == nil || b == nil {
		return a == b
	}
	return slices.Equal(a.Routed, b.Routed) && slices.Equal(a.Orphans, b.Orphans)
}

// orphanWarnings says which DNS records still point at the project's tunnel
// although their services are gone.
func orphanWarnings(tunnel *state.Tunnel) []string {
	if tunnel == nil {
		return nil
	}
	var out []string
	for _, name := range tunnel.Orphans {
		out = append(out, fmt.Sprintf("cloudflare: %s still points at tunnel %s, but no service uses it; delete its DNS record in the Cloudflare dashboard", name, tunnel.Name))
	}
	return out
}

// cloudflareWarnings explains what stops the project's Cloudflare services
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

// refuseCloudflare stops pier share and unshare, which switch Tailscale
// Funnel, on a service Cloudflare serves: it is always public there.
func (s *Service) refuseCloudflare(start, name, command string) error {
	loaded, err := s.loadProject(start, true)
	if err != nil {
		return err
	}
	service, err := findService(loaded.cfg, name)
	if err != nil {
		return err
	}
	if !service.OnTailscale() {
		return fmt.Errorf("%s is served by Cloudflare at %s, which is always public; pier %s works on Tailscale services only", name, service.Cloudflare, command)
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
