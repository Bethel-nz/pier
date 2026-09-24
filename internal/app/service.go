// Package app implements the shared Pier CLI/TUI use cases.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Bethel-nz/pier/internal/config"
	"github.com/Bethel-nz/pier/internal/health"
	"github.com/Bethel-nz/pier/internal/localname"
	"github.com/Bethel-nz/pier/internal/platform"
	"github.com/Bethel-nz/pier/internal/project"
	"github.com/Bethel-nz/pier/internal/reconcile"
	"github.com/Bethel-nz/pier/internal/state"
	"github.com/Bethel-nz/pier/internal/tailscale"
)

// ValidateRequest locates and validates a project configuration.
type ValidateRequest struct {
	Start string
}

// ValidateResult contains schema and semantic validation findings.
type ValidateResult struct {
	Project project.Context
	Config  config.Project
	Errors  []config.ValidationError
}

// PlanRequest previews reconciliation without mutating Tailscale.
type PlanRequest struct {
	Start string
	Force bool
}

// PlanResult is the non-mutating desired-versus-actual plan.
type PlanResult struct {
	Project project.Context
	Plan    reconcile.Plan
	// TailscaleSkipped says why Tailscale was not planned, when it is unavailable.
	TailscaleSkipped string
}

// UpRequest applies the project plan to Tailscale.
type UpRequest struct {
	Start  string
	Force  bool
	Strict bool
}

// UpResult contains the applied plan, URLs, and local health.
type UpResult struct {
	Project  project.Context
	Plan     reconcile.Plan
	Services []ServiceInfo
	// Local describes .local serving: certificate, trust, port, and names.
	Local localname.Report
	// TailscaleSkipped says why Tailscale was left alone. Local names were still served.
	TailscaleSkipped string
	// Warnings are public routes on this machine that this project does not manage.
	Warnings []string
	// Cloudflare is what this run set up in Cloudflare for cloudflare: hostnames.
	Cloudflare TunnelSetup
}

// DownRequest removes routes owned by the current project.
type DownRequest struct {
	Start string
}

// DownResult contains the deletion plan for owned routes.
type DownResult struct {
	Project project.Context
	Plan    reconcile.Plan
	// TailscaleSkipped says why Tailscale routes were left in place.
	TailscaleSkipped string
	// KeptRoutes counts owned Tailscale routes left in place because Tailscale was skipped.
	KeptRoutes int
	// TunnelStopped names the Cloudflare Tunnel that stopped serving, if any.
	TunnelStopped string
	// NamesWithdrawn is set when the project's .local names stopped being served.
	NamesWithdrawn bool
}

// StatusRequest reads configured services, health, and URLs.
type StatusRequest struct {
	Start string
}

// StatusResult is the current configured service view.
type StatusResult struct {
	Project  project.Context
	DNSName  string
	Services []ServiceInfo
	Local    localname.Report
	// TailscaleSkipped says why no Tailscale URLs are shown.
	TailscaleSkipped string
	// Warnings are public routes worth a second look, on this whole machine.
	Warnings []string
}

// DoctorRequest diagnoses configuration, Tailscale, and local targets.
type DoctorRequest struct {
	Start string
}

// DoctorResult collects diagnostic data without treating health as fatal.
type DoctorResult struct {
	Project      project.Context
	Config       config.Project
	Validation   []config.ValidationError
	Capabilities tailscale.Capabilities
	TailscaleErr error
	Health       []health.Result
	// Local is nil when no service declares a domain.
	Local *localname.Report
	// Warnings are public routes worth a second look, and install problems.
	Warnings []string
}

// ShareRequest applies a runtime public-access override.
type ShareRequest struct {
	Start   string
	Service string
	Force   bool
	Strict  bool
}

// ShareResult is the reconciled public URL for one service.
type ShareResult struct {
	Project project.Context
	Service ServiceInfo
	Plan    reconcile.Plan
}

// UnshareRequest removes a runtime public-access override.
type UnshareRequest struct {
	Start   string
	Service string
	Force   bool
	Strict  bool
}

// UnshareResult is the reconciled URL after restoring configured access.
type UnshareResult struct {
	Project project.Context
	Service ServiceInfo
	Plan    reconcile.Plan
}

// OperationResult is a completed mutating action for the TUI.
type OperationResult struct {
	Command  string
	Plan     reconcile.Plan
	Services []ServiceInfo
}

// ServiceInfo is one configured service as presented to CLI and TUI.
type ServiceInfo struct {
	Name      string
	Target    string
	Host      string
	Port      uint16
	HTTPSPort uint16
	Path      string
	Public    bool
	Paused    bool
	URL       string
	Domain    string
	// LocalURL is set only while Pier is actually serving Domain.
	LocalURL string
	// LANURL is the plain-HTTP fallback on this machine's LAN IP, for devices
	// that cannot resolve Domain.
	LANURL string
	// LocalState is live, probing, conflict, no-certificate, mdns-unavailable, or down.
	LocalState string
	Health     health.Result
	// TCP is set for a raw TCP service, reached at host:port rather than a URL path.
	TCP bool
	// Cloudflare is the service's public hostname on Cloudflare, if any.
	Cloudflare string
	// CloudflareURL is set only while the tunnel is connected.
	CloudflareURL string
	// CloudflareState is connected, connecting, failed, paused, or down.
	CloudflareState  string
	CloudflareDetail string
	// PublicSince is when the service's live Funnel route was made; zero if
	// not public or unknown.
	PublicSince time.Time
	// PublicUntil is when a timed public window closes; zero otherwise.
	PublicUntil time.Time
	// Drift lists where Tailscale differs from pier.yaml, each with its fix.
	// pier status fills it; other commands just made the two agree.
	Drift []string
}

// InvalidConfigError is aggregated configuration validation failure.
type InvalidConfigError struct {
	Errors []config.ValidationError
}

func (e *InvalidConfigError) Error() string {
	return "Pier configuration is invalid"
}

// PrerequisiteError is a Tailscale diagnostic failure that blocks mutation.
type PrerequisiteError struct {
	Err error
}

func (e *PrerequisiteError) Error() string {
	if e.Err == nil {
		return "Pier cannot use Tailscale"
	}
	msg := e.Err.Error()
	if strings.HasPrefix(msg, "Pier ") {
		return msg
	}
	return "Pier cannot use Tailscale: " + msg
}

func (e *PrerequisiteError) Unwrap() error { return e.Err }

// ConflictError is an unmanaged Tailscale route that blocks apply unless forced.
type ConflictError struct {
	Conflicts []reconcile.Conflict
}

func (e *ConflictError) Error() string {
	return "Pier refused to change unmanaged Tailscale routes; use --force to take them over"
}

// ApplyError is a Tailscale mutation or verification failure.
type ApplyError struct {
	Err    error
	Result reconcile.ApplyResult
}

func (e *ApplyError) Error() string {
	if e.Err == nil {
		return "Pier could not apply Tailscale route changes"
	}
	msg := e.Err.Error()
	if strings.HasPrefix(msg, "Pier ") {
		return msg
	}
	return "Pier could not apply Tailscale route changes: " + msg
}

func (e *ApplyError) Unwrap() error { return e.Err }

// UnavailableTargetsError is a --strict failure before apply.
type UnavailableTargetsError struct {
	Results []health.Result
}

func (e *UnavailableTargetsError) Error() string {
	return "Pier refused to apply routes because a local target is unavailable"
}

// ServiceNotFoundError is a missing service name in the project configuration.
type ServiceNotFoundError struct {
	Name string
}

func (e *ServiceNotFoundError) Error() string {
	return fmt.Sprintf("Pier could not find a service named %q", e.Name)
}

// NotConfiguredError is a service that exists in pier.yaml but not in Tailscale.
type NotConfiguredError struct {
	Name string
}

func (e *NotConfiguredError) Error() string {
	return fmt.Sprintf("Pier cannot open or copy %q because it is not currently configured in Tailscale", e.Name)
}

// ServiceExistsError is a duplicate service name in pier.yaml.
type ServiceExistsError struct {
	Name string
}

func (e *ServiceExistsError) Error() string {
	return fmt.Sprintf("Pier already has a service named %q", e.Name)
}

// ServicePausedError is a paused service that currently has no Tailscale route.
type ServicePausedError struct {
	Name string
}

func (e *ServicePausedError) Error() string {
	return fmt.Sprintf("Pier cannot open or copy %q because it is paused", e.Name)
}

// PauseRequest removes a service's Tailscale route without touching the local process.
type PauseRequest struct {
	Start   string
	Service string
	Force   bool
	Strict  bool
}

// PauseResult is the service after its route is paused.
type PauseResult struct {
	Project project.Context
	Service ServiceInfo
	Plan    reconcile.Plan
}

// ResumeRequest restores a paused service's Tailscale route.
type ResumeRequest struct {
	Start   string
	Service string
	Force   bool
	Strict  bool
}

// ResumeResult is the service after its route is restored.
type ResumeResult struct {
	Project project.Context
	Service ServiceInfo
	Plan    reconcile.Plan
}

// AddServiceRequest appends a service to pier.yaml.
type AddServiceRequest struct {
	Start    string
	Name     string
	Target   string
	Path     string
	Public   bool
	Protocol string
}

// AddServiceResult is the newly written service.
type AddServiceResult struct {
	Project project.Context
	Service ServiceInfo
}

// Store persists owned routes and runtime overrides.
type Store interface {
	Load(projectID string) (state.ProjectState, error)
	Save(state.ProjectState) error
	Delete(projectID string) error
}

// Tailscale is the apply/read adapter used by the application service.
type Tailscale interface {
	Check(ctx context.Context) (tailscale.Capabilities, error)
	Status(ctx context.Context) (tailscale.Status, error)
	DNSName() string
	Apply(ctx context.Context, op reconcile.Operation) error
	Routes(ctx context.Context) ([]reconcile.Route, error)
}

// Service is the shared CLI/TUI facade.
type Service struct {
	find       func(string) (project.Context, error)
	load       func(string) (config.Config, error)
	store      Store
	ts         Tailscale
	health     func(context.Context, config.ResolvedService) health.Result
	build      func(desired, actual, owned []reconcile.Route, force bool) reconcile.Plan
	afterPlan  func(plan reconcile.Plan, force bool) error
	now        func() time.Time
	openURL    func(context.Context, string) error
	copyURL    func(context.Context, string) error
	addService func(path, name string, service config.Service) error
	locals     LocalNames
	tunnels    Tunnels
}

// LocalNames serves .local domains: certificates, trust, and the daemon.
type LocalNames interface {
	Sync(ctx context.Context, root string, names []string, settings state.LocalSettings) (localname.Report, error)
	Status() localname.Report
}

// EnableLocalNames serves service domains on the local network.
func (s *Service) EnableLocalNames(names LocalNames) {
	s.locals = names
}

// New constructs the production application service.
func New(store Store, runner tailscale.Runner) *Service {
	if runner == nil {
		runner = tailscale.ExecRunner{}
	}
	actions := platform.Actions{}
	return &Service{
		find:       project.Find,
		load:       config.Load,
		store:      store,
		ts:         newLiveTailscale(runner),
		health:     health.Check,
		build:      reconcile.Build,
		now:        time.Now,
		openURL:    actions.Open,
		copyURL:    actions.Copy,
		addService: config.AddService,
	}
}

// Validate loads and validates configuration without requiring running services.
func (s *Service) Validate(_ context.Context, req ValidateRequest) (ValidateResult, error) {
	loaded, err := s.loadProject(req.Start, false)
	result := ValidateResult{Project: loaded.project, Config: loaded.cfg, Errors: loaded.validation}
	if err != nil {
		return result, err
	}
	if len(loaded.validation) > 0 {
		return result, &InvalidConfigError{Errors: loaded.validation}
	}
	return result, nil
}

// Plan returns the same reconciliation plan Up would apply.
func (s *Service) Plan(ctx context.Context, req PlanRequest) (PlanResult, error) {
	sess, err := s.prepare(ctx, req.Start)
	result := PlanResult{Project: sess.project}
	if err != nil {
		return result, err
	}
	// Plan shows what pier up would do, which opens a fresh public window.
	sess.cfg = withExpiry(sess.cfg, renewWindows(sess.cfg, s.clock()), s.clock())
	taps, err := assignTaps(sess.cfg, sess.state.Taps, sess.state.Paused)
	if err != nil {
		return result, err
	}
	sess.plan = s.plannerFor(&sess, desiredRoutes(sess.project, sess.cfg, sess.state.Overrides, sess.state.Paused, taps), ownedRoutes(sess.project, sess.state), req.Force)
	result.Plan = sess.plan
	result.TailscaleSkipped = sess.tailscaleSkipped
	return result, s.reviewPlan(sess.plan, req.Force)
}

// Up validates, plans, applies, verifies, and persists owned routes.
func (s *Service) Up(ctx context.Context, req UpRequest) (UpResult, error) {
	return s.reconcile(ctx, req.Start, req.Force, req.Strict, true, nil, "")
}

// Down deletes only routes recorded as owned by this project.
func (s *Service) Down(ctx context.Context, req DownRequest) (DownResult, error) {
	sess, err := s.prepare(ctx, req.Start)
	result := DownResult{Project: sess.project}
	if err != nil {
		return result, err
	}
	sess.plan = s.plannerFor(&sess, nil, ownedRoutes(sess.project, sess.state), false)
	result.Plan = sess.plan
	result.TailscaleSkipped = sess.tailscaleSkipped
	if sess.tailscaleSkipped != "" {
		result.KeptRoutes = len(sess.state.Routes)
	}
	if err := s.reviewPlan(sess.plan, false); err != nil {
		return result, err
	}
	serving := sess.state.Tunnel
	result.NamesWithdrawn = len(sess.state.Domains) > 0
	if err := s.applyAndPersist(ctx, &sess, sess.state.Overrides, sess.state.Paused, false); err != nil {
		return result, err
	}
	if serving.Serving() {
		result.TunnelStopped = serving.Name
	}
	return result, nil
}

// Status reports configured services, health, public access, and URLs.
func (s *Service) Status(ctx context.Context, req StatusRequest) (StatusResult, error) {
	loaded, err := s.loadProject(req.Start, true)
	if err != nil {
		return StatusResult{Project: loaded.project}, err
	}
	st, err := s.store.Load(loaded.project.ID)
	if err != nil {
		return StatusResult{Project: loaded.project}, fmt.Errorf("Pier could not load project state: %w", err)
	}
	now := s.clock()
	services := effectiveServices(withExpiry(loaded.cfg, st.PublicUntil, now), st.Overrides)
	results := s.checkTargets(ctx, services)
	result := StatusResult{Project: loaded.project}
	// URLs come from what Tailscale serves now, never from what was saved.
	if _, err := s.ts.Check(ctx); err != nil {
		result.TailscaleSkipped = (&PrerequisiteError{Err: err}).Error()
		result.Services = serviceInfos(services, "", indexHealth(results), st.Paused)
		for i := range result.Services {
			result.Services[i].Public = false
		}
	} else {
		actual, err := s.ts.Routes(ctx)
		if err != nil {
			return result, fmt.Errorf("Pier could not read Tailscale routes: %w", err)
		}
		result.DNSName = s.lookupDNS(ctx, "")
		result.Services = serviceInfos(services, result.DNSName, indexHealth(results), st.Paused)
		withLiveRoutes(result.Services, services, actual, st.Routes, result.DNSName, st.Paused)
		withPublicUntil(result.Services, st.PublicUntil, st.Overrides, now)
		result.Warnings = publicWarnings(actual, s.owners(), result.DNSName, now, nil)
	}
	if s.locals != nil && (hasDomains(loaded.cfg) || loaded.cfg.HasCloudflare()) {
		result.Local = s.locals.Status()
		withLocal(result.Services, result.Local)
		withTunnel(result.Services, result.Local, loaded.project.ID)
	}
	return result, nil
}

// Doctor diagnoses config, Tailscale, and local targets as structured data.
func (s *Service) Doctor(ctx context.Context, req DoctorRequest) (DoctorResult, error) {
	var result DoctorResult
	loaded, loadErr := s.loadProject(req.Start, false)
	result.Project = loaded.project
	result.Config = loaded.cfg
	result.Validation = loaded.validation
	if loadErr == nil && len(loaded.cfg.Services) > 0 {
		result.Health = s.checkTargets(ctx, loaded.cfg.Services)
	}
	caps, tsErr := s.ts.Check(ctx)
	result.Capabilities = caps
	result.TailscaleErr = tsErr
	if tsErr == nil {
		if actual, err := s.ts.Routes(ctx); err == nil {
			result.Warnings = publicWarnings(actual, s.owners(), s.lookupDNS(ctx, ""), s.clock(), nil)
		}
	}
	if mismatch := pathMismatch(); mismatch != "" {
		result.Warnings = append(result.Warnings, mismatch)
	}
	if s.locals != nil && hasDomains(loaded.cfg) {
		local := s.locals.Status()
		result.Local = &local
	}
	if loadErr == nil && loaded.cfg.HasCloudflare() {
		result.Warnings = append(result.Warnings, s.cloudflareWarnings(loaded.project.ID)...)
	}
	return result, loadErr
}

// Share exposes a service through Funnel using the normal reconcile path.
func (s *Service) Share(ctx context.Context, req ShareRequest) (ShareResult, error) {
	if err := s.refuseCloudflare(req.Start, req.Service, "share"); err != nil {
		return ShareResult{}, err
	}
	up, err := s.reconcile(ctx, req.Start, req.Force, req.Strict, false, func(overrides, _ map[string]bool) error {
		overrides[req.Service] = true
		return nil
	}, req.Service)
	if err == nil && up.TailscaleSkipped != "" {
		err = &PrerequisiteError{Err: fmt.Errorf("sharing needs Tailscale Funnel: %s", strings.TrimPrefix(up.TailscaleSkipped, "Pier cannot use Tailscale: "))}
	}
	return ShareResult{Project: up.Project, Plan: up.Plan, Service: lookupService(up.Services, req.Service)}, err
}

// Unshare restores the configured public value using the normal reconcile path.
func (s *Service) Unshare(ctx context.Context, req UnshareRequest) (UnshareResult, error) {
	if err := s.refuseCloudflare(req.Start, req.Service, "unshare"); err != nil {
		return UnshareResult{}, err
	}
	up, err := s.reconcile(ctx, req.Start, req.Force, req.Strict, false, func(overrides, _ map[string]bool) error {
		delete(overrides, req.Service)
		return nil
	}, req.Service)
	if err == nil && up.TailscaleSkipped != "" {
		err = &PrerequisiteError{Err: fmt.Errorf("unsharing needs Tailscale: %s", strings.TrimPrefix(up.TailscaleSkipped, "Pier cannot use Tailscale: "))}
	}
	return UnshareResult{Project: up.Project, Plan: up.Plan, Service: lookupService(up.Services, req.Service)}, err
}

// Pause removes a service route from Tailscale and leaves the local process running.
func (s *Service) Pause(ctx context.Context, req PauseRequest) (PauseResult, error) {
	up, err := s.reconcile(ctx, req.Start, req.Force, req.Strict, false, func(_, paused map[string]bool) error {
		paused[req.Service] = true
		return nil
	}, req.Service)
	return PauseResult{Project: up.Project, Plan: up.Plan, Service: lookupService(up.Services, req.Service)}, err
}

// Resume restores a paused service route through the normal reconcile path.
func (s *Service) Resume(ctx context.Context, req ResumeRequest) (ResumeResult, error) {
	up, err := s.reconcile(ctx, req.Start, req.Force, req.Strict, false, func(_, paused map[string]bool) error {
		delete(paused, req.Service)
		return nil
	}, req.Service)
	return ResumeResult{Project: up.Project, Plan: up.Plan, Service: lookupService(up.Services, req.Service)}, err
}

// AddService writes a new service into pier.yaml without changing Tailscale routes.
func (s *Service) AddService(ctx context.Context, req AddServiceRequest) (AddServiceResult, error) {
	loaded, err := s.loadProject(req.Start, true)
	result := AddServiceResult{Project: loaded.project}
	if err != nil {
		return result, err
	}
	if _, err := findService(loaded.cfg, req.Name); err == nil {
		return result, &ServiceExistsError{Name: req.Name}
	}
	write := s.addService
	if write == nil {
		write = config.AddService
	}
	service := config.Service{Target: req.Target, Path: req.Path, Protocol: req.Protocol}
	service.Public = config.PublicFlag(req.Public)
	if err := write(loaded.project.ConfigPath, req.Name, service); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return result, &ServiceExistsError{Name: req.Name}
		}
		return result, fmt.Errorf("Pier could not update project configuration: %w", err)
	}
	reloaded, err := s.loadProject(req.Start, true)
	if err != nil {
		return result, err
	}
	st, _ := s.store.Load(reloaded.project.ID)
	found, err := findService(reloaded.cfg, req.Name)
	if err != nil {
		return result, err
	}
	infos := serviceInfos([]config.ResolvedService{found}, s.lookupDNS(ctx, st.DNSName), nil, st.Paused)
	result.Project = reloaded.project
	result.Service = infos[0]
	return result, nil
}

// OpenRequest looks up a service URL for the platform opener.
type OpenRequest struct {
	Start   string
	Service string
	// Local selects the .local URL instead of the Tailscale URL.
	Local bool
}

// OpenResult is the resolved service URL.
type OpenResult struct {
	URL string
}

// CopyRequest looks up a service URL for the clipboard.
type CopyRequest struct {
	Start   string
	Service string
	// Local selects the .local URL instead of the Tailscale URL.
	Local bool
}

// CopyResult is the resolved service URL after a successful copy.
type CopyResult struct {
	URL string
}

// Open resolves the current URL for a named service and opens it.
func (s *Service) Open(ctx context.Context, req OpenRequest) (OpenResult, error) {
	url, err := s.lookupConfiguredURL(ctx, req.Start, req.Service, req.Local)
	if err != nil {
		return OpenResult{}, err
	}
	if strings.HasPrefix(url, "tcp://") {
		return OpenResult{URL: url}, fmt.Errorf("%s is a TCP service, which a browser cannot open; connect to %s with its client, or pier copy %s", req.Service, strings.TrimPrefix(url, "tcp://"), req.Service)
	}
	if s.openURL != nil {
		if err := s.openURL(ctx, url); err != nil {
			return OpenResult{URL: url}, err
		}
	}
	return OpenResult{URL: url}, nil
}

// Copy resolves the current URL for a named service and copies it.
func (s *Service) Copy(ctx context.Context, req CopyRequest) (CopyResult, error) {
	url, err := s.lookupConfiguredURL(ctx, req.Start, req.Service, req.Local)
	if err != nil {
		return CopyResult{}, err
	}
	if s.copyURL != nil {
		if err := s.copyURL(ctx, url); err != nil {
			return CopyResult{URL: url}, err
		}
	}
	return CopyResult{URL: url}, nil
}

func (s *Service) lookupConfiguredURL(ctx context.Context, start, name string, local bool) (string, error) {
	status, err := s.Status(ctx, StatusRequest{Start: start})
	if err != nil {
		return "", err
	}
	var found *ServiceInfo
	for i := range status.Services {
		if status.Services[i].Name == name {
			found = &status.Services[i]
			break
		}
	}
	if found == nil {
		return "", &ServiceNotFoundError{Name: name}
	}
	if found.Paused {
		return "", &ServicePausedError{Name: name}
	}
	if local {
		if found.Domain == "" {
			return "", fmt.Errorf("Pier has no .local name for %q; add local: to it in pier.yaml", name)
		}
		if found.LocalURL == "" {
			return "", fmt.Errorf("Pier is not serving %s right now (%s); run pier up", found.Domain, found.LocalState)
		}
		return found.LocalURL, nil
	}
	actual, err := s.ts.Routes(ctx)
	if err != nil {
		return "", fmt.Errorf("Pier could not read Tailscale routes: %w", err)
	}
	key := reconcile.Route{HTTPSPort: found.HTTPSPort, Path: found.Path, TCP: found.TCP}.Key()
	for _, route := range actual {
		if route.Key() == key {
			return found.URL, nil
		}
	}
	return "", &NotConfiguredError{Name: name}
}

// reconcile applies the project's routes. renew opens a fresh window for each
// timed public service, which only pier up does.
func (s *Service) reconcile(ctx context.Context, start string, force, strict, renew bool, mutate func(overrides, paused map[string]bool) error, focus string) (UpResult, error) {
	sess, err := s.prepare(ctx, start)
	result := UpResult{Project: sess.project}
	if err != nil {
		return result, err
	}
	now := s.clock()
	sess.until = sess.state.PublicUntil
	if renew {
		sess.until = renewWindows(sess.cfg, now)
	}
	sess.cfg = withExpiry(sess.cfg, sess.until, now)
	if focus != "" {
		if _, err := findService(sess.cfg, focus); err != nil {
			return result, err
		}
	}
	overrides := copyBoolMap(sess.state.Overrides)
	paused := copyBoolMap(sess.state.Paused)
	if mutate != nil {
		if err := mutate(overrides, paused); err != nil {
			return result, err
		}
	}
	services := effectiveServices(sess.cfg, overrides)
	if sess.taps, err = assignTaps(sess.cfg, sess.state.Taps, paused); err != nil {
		return result, err
	}
	sess.plan = s.plannerFor(&sess, desiredRoutes(sess.project, sess.cfg, overrides, paused, sess.taps), ownedRoutes(sess.project, sess.state), force)
	result.Plan = sess.plan
	result.TailscaleSkipped = sess.tailscaleSkipped
	if err := s.reviewPlan(sess.plan, force); err != nil {
		return result, err
	}
	healthResults := s.checkTargets(ctx, services)
	result.Services = serviceInfos(services, sess.dns, indexHealth(healthResults), paused)
	withPublicUntil(result.Services, sess.until, overrides, now)
	if strict {
		if err := strictHealth(healthResults); err != nil {
			return result, err
		}
	}
	result.Warnings = append(s.foreignPublic(&sess), untimedPublic(sess.plan, sess.cfg, overrides)...)
	if sess.tunnel, result.Cloudflare, err = s.setupTunnel(ctx, &sess, paused, force); err != nil {
		return result, err
	}
	err = s.applyAndPersist(ctx, &sess, overrides, paused, true)
	result.Local = sess.local
	withLocal(result.Services, sess.local)
	withTunnel(result.Services, sess.local, sess.project.ID)
	return result, err
}

// foreignPublic warns about public routes on this machine that this project
// neither owns nor is about to create: the ones pier up would silently leave.
func (s *Service) foreignPublic(sess *session) []string {
	if sess.tailscaleSkipped != "" {
		return nil
	}
	mine := map[string]bool{}
	for _, route := range sess.state.Routes {
		mine[ownedRoute(route).Key()] = true
	}
	for _, op := range sess.plan.Operations {
		mine[op.After.Key()] = true
	}
	return publicWarnings(sess.actual, s.owners(), sess.dns, s.clock(), mine)
}

type session struct {
	project project.Context
	cfg     config.Project
	state   state.ProjectState
	actual  []reconcile.Route
	dns     string
	plan    reconcile.Plan
	local   localname.Report
	// taps are the services Pier throttles or captures after this change.
	taps []state.Tap
	// until is when each timed public window closes after this change.
	until map[string]time.Time
	// tunnel is the Cloudflare Tunnel after this change.
	tunnel *state.Tunnel
	// tailscaleSkipped says why Tailscale was left alone, when it is unavailable
	// and the project still has local names to serve.
	tailscaleSkipped string
}

// plannerFor builds the Tailscale plan, or an empty one when Tailscale is skipped.
func (s *Service) plannerFor(sess *session, desired, owned []reconcile.Route, force bool) reconcile.Plan {
	if sess.tailscaleSkipped != "" {
		return reconcile.Plan{}
	}
	return s.planner(desired, sess.actual, owned, force)
}

type loadedProject struct {
	project    project.Context
	cfg        config.Project
	validation []config.ValidationError
}

func (s *Service) loadProject(start string, requireValid bool) (loadedProject, error) {
	var out loadedProject
	proj, err := s.find(start)
	if err != nil {
		return out, err
	}
	out.project = proj
	raw, err := s.load(proj.ConfigPath)
	if err != nil {
		return out, fmt.Errorf("Pier could not read project configuration: %w", err)
	}
	cfg, err := config.Normalize(raw)
	if err != nil {
		return out, fmt.Errorf("Pier could not normalize project configuration: %w", err)
	}
	out.cfg = cfg
	out.validation = config.Validate(cfg)
	if requireValid && len(out.validation) > 0 {
		return out, &InvalidConfigError{Errors: out.validation}
	}
	return out, nil
}

func (s *Service) prepare(ctx context.Context, start string) (session, error) {
	var sess session
	loaded, err := s.loadProject(start, true)
	sess.project = loaded.project
	sess.cfg = loaded.cfg
	if err != nil {
		return sess, err
	}
	if _, err := s.ts.Check(ctx); err != nil {
		// Local names and Cloudflare do not need Tailscale: serve them and report Tailscale as skipped.
		if !hasDomains(loaded.cfg) && !loaded.cfg.HasCloudflare() {
			return sess, &PrerequisiteError{Err: err}
		}
		sess.tailscaleSkipped = (&PrerequisiteError{Err: err}).Error()
		st, loadErr := s.store.Load(loaded.project.ID)
		if loadErr != nil {
			return sess, fmt.Errorf("Pier could not load project state: %w", loadErr)
		}
		sess.state = st
		return sess, nil
	}
	actual, err := s.ts.Routes(ctx)
	if err != nil {
		return sess, fmt.Errorf("Pier could not read Tailscale routes: %w", err)
	}
	sess.actual = actual
	sess.dns = strings.TrimRight(s.ts.DNSName(), ".")
	if sess.dns == "" {
		if st, err := s.ts.Status(ctx); err == nil {
			sess.dns = strings.TrimRight(st.DNSName, ".")
		}
	}
	st, err := s.store.Load(loaded.project.ID)
	if err != nil {
		return sess, fmt.Errorf("Pier could not load project state: %w", err)
	}
	sess.state = st
	return sess, nil
}

func (s *Service) lookupDNS(ctx context.Context, fallback string) string {
	if _, err := s.ts.Check(ctx); err == nil {
		if dns := strings.TrimRight(s.ts.DNSName(), "."); dns != "" {
			return dns
		}
		if st, err := s.ts.Status(ctx); err == nil {
			if dns := strings.TrimRight(st.DNSName, "."); dns != "" {
				return dns
			}
		}
	}
	return strings.TrimRight(fallback, ".")
}

func (s *Service) planner(desired, actual, owned []reconcile.Route, force bool) reconcile.Plan {
	if s.build != nil {
		return s.build(desired, actual, owned, force)
	}
	return reconcile.Build(desired, actual, owned, force)
}

func (s *Service) reviewPlan(plan reconcile.Plan, force bool) error {
	if s.afterPlan != nil {
		return s.afterPlan(plan, force)
	}
	return rejectConflicts(plan, force)
}

func rejectConflicts(plan reconcile.Plan, force bool) error {
	if force || len(plan.Conflicts) == 0 {
		return nil
	}
	return &ConflictError{Conflicts: plan.Conflicts}
}

func (s *Service) checkTargets(ctx context.Context, services []config.ResolvedService) []health.Result {
	check := s.health
	if check == nil {
		check = health.Check
	}
	results := make([]health.Result, 0, len(services))
	for _, service := range services {
		result := check(ctx, service)
		if result.Service == "" {
			result.Service = service.Name
		}
		results = append(results, result)
	}
	return results
}

func (s *Service) applyAndPersist(ctx context.Context, sess *session, overrides, paused map[string]bool, keepDomains bool) error {
	domains := []state.LocalDomain(nil)
	taps := []state.Tap(nil)
	var until map[string]time.Time
	tunnel := idleTunnel(sess.state.Tunnel)
	if keepDomains {
		domains = localDomains(sess.cfg, paused, sess.state.Domains)
		taps = sess.taps
		until = sess.until
		tunnel = sess.tunnel
	}
	hadWork := needsDaemon(sess.state)
	// Refuse a taken name before Tailscale changes, so a rejected up leaves nothing half-applied.
	if err := s.rejectDomainConflicts(sess.project.ID, domains); err != nil {
		return err
	}
	result, err := reconcile.Apply(ctx, sess.plan, s.ts)
	if err != nil {
		return &ApplyError{Err: err, Result: result}
	}
	settings := state.LocalSettings{}
	if keepDomains {
		settings = localSettings(sess.project.Root, sess.cfg.Local)
	}
	pausedChanged := !maps.Equal(compactBoolMap(sess.state.Paused), compactBoolMap(paused))
	domainsChanged := !sameDomains(sess.state.Domains, domains) || (len(domains) > 0 && sess.state.Path != sess.project.Root) ||
		sess.state.Local != settings
	tapsChanged := !sameTaps(sess.state.Taps, taps) || (len(taps) > 0 && sess.state.Path != sess.project.Root) ||
		!sameTimes(sess.state.PublicUntil, until)
	tunnelChanged := !sess.state.Tunnel.Equal(tunnel) || (tunnel.Serving() && sess.state.Path != sess.project.Root)
	if result.Verified == nil && !pausedChanged && !domainsChanged && !tapsChanged && !tunnelChanged {
		// Nothing to save, but the daemon may have stopped since: make it serve again.
		return s.syncLocal(ctx, sess, domains, hadWork)
	}
	st := sess.state
	st.Version = state.CurrentVersion
	st.ProjectID = sess.project.ID
	if sess.cfg.Name != "" {
		st.Name = sess.cfg.Name
	}
	st.Path = sess.project.Root
	st.DNSName = sess.dns
	st.ConfigHash = configHash(sess.project.ConfigPath)
	if result.Verified != nil {
		st.Routes = nextOwned(st.Routes, result.Completed, s.clock())
		st.Overrides = overrides
	}
	st.Paused = compactBoolMap(paused)
	st.Domains = domains
	st.Taps = taps
	st.PublicUntil = until
	st.Tunnel = tunnel
	st.Local = settings
	st.UpdatedAt = s.clock()
	if err := s.store.Save(st); err != nil {
		return fmt.Errorf("Pier could not save project state: %w", err)
	}
	sess.state = st
	return s.syncLocal(ctx, sess, domains, hadWork)
}

// syncLocal hands saved domains, taps, and public windows to the daemon.
// Projects with none skip it, unless they just dropped their last one.
func (s *Service) syncLocal(ctx context.Context, sess *session, domains []state.LocalDomain, hadWork bool) error {
	if s.locals == nil || (len(domains) == 0 && !hasDomains(sess.cfg) && !needsDaemon(sess.state) && !hadWork) {
		return nil
	}
	names := make([]string, 0, len(domains))
	for _, domain := range domains {
		names = append(names, domain.Name)
	}
	report, err := s.locals.Sync(ctx, sess.project.Root, names, sess.state.Local)
	sess.local = report
	if err != nil {
		return fmt.Errorf("Pier could not serve local domains: %w", err)
	}
	return nil
}

// localSettings turns the local: block into saved state, resolving a
// bring-your-own certificate against the project root.
func localSettings(root string, local config.Local) state.LocalSettings {
	settings := state.LocalSettings{ThisMachineOnly: !local.LAN, Autostart: local.Autostart}
	if local.CertFile != "" && local.KeyFile != "" {
		settings.CertFile = absolute(root, local.CertFile)
		settings.KeyFile = absolute(root, local.KeyFile)
	}
	return settings
}

func absolute(root, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, path)
}

func strictHealth(results []health.Result) error {
	var failed []health.Result
	for _, result := range results {
		if result.Status != health.StatusHealthy {
			failed = append(failed, result)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return &UnavailableTargetsError{Results: failed}
}

func findService(cfg config.Project, name string) (config.ResolvedService, error) {
	for _, service := range cfg.Services {
		if service.Name == name {
			return service, nil
		}
	}
	return config.ResolvedService{}, &ServiceNotFoundError{Name: name}
}

func lookupService(services []ServiceInfo, name string) ServiceInfo {
	for _, service := range services {
		if service.Name == name {
			return service
		}
	}
	return ServiceInfo{Name: name}
}

func effectiveServices(cfg config.Project, overrides map[string]bool) []config.ResolvedService {
	services := append([]config.ResolvedService(nil), cfg.Services...)
	for i, service := range services {
		public, ok := overrides[service.Name]
		if !ok || !service.OnTailscale() {
			continue
		}
		services[i].Public = public
		if public {
			services[i].HTTPSPort = 443
		} else {
			services[i].HTTPSPort = 8443
		}
	}
	return services
}

func desiredRoutes(proj project.Context, cfg config.Project, overrides, paused map[string]bool, taps []state.Tap) []reconcile.Route {
	services := throughTaps(effectiveServices(cfg, overrides), taps)
	active := make([]config.ResolvedService, 0, len(services))
	for _, service := range services {
		if paused[service.Name] {
			continue
		}
		active = append(active, service)
	}
	return routesFromServices(proj, active)
}

func routesFromServices(proj project.Context, services []config.ResolvedService) []reconcile.Route {
	routes := make([]reconcile.Route, 0, len(services))
	for _, service := range services {
		if !service.OnTailscale() {
			continue
		}
		routes = append(routes, reconcile.Route{
			Service:   service.Name,
			ProjectID: proj.ID,
			HTTPSPort: service.HTTPSPort,
			Path:      service.Path,
			Target:    service.Target,
			Public:    service.Public,
			TCP:       service.TCP(),
		})
	}
	return routes
}

func ownedRoutes(proj project.Context, st state.ProjectState) []reconcile.Route {
	routes := make([]reconcile.Route, 0, len(st.Routes))
	for _, route := range st.Routes {
		owned := ownedRoute(route)
		owned.ProjectID = proj.ID
		routes = append(routes, owned)
	}
	return routes
}

// ownedRoute is a saved route as the planner sees it. Every route key built
// from saved state goes through here, so HTTPS and TCP routes key alike.
func ownedRoute(route state.Route) reconcile.Route {
	return reconcile.Route{Service: route.Service, HTTPSPort: route.HTTPSPort, Path: route.Path, TCP: route.TCP}
}

func nextOwned(prev []state.Route, completed []reconcile.Operation, now time.Time) []state.Route {
	byKey := make(map[string]state.Route, len(prev)+len(completed))
	for _, route := range prev {
		byKey[ownedRoute(route).Key()] = route
	}
	for _, op := range completed {
		switch op.Kind {
		case reconcile.KindDelete:
			delete(byKey, op.Before.Key())
		case reconcile.KindCreate, reconcile.KindUpdate:
			byKey[op.After.Key()] = state.Route{
				Service:   op.After.Service,
				HTTPSPort: op.After.HTTPSPort,
				Path:      op.After.Path,
				Public:    op.After.Public,
				TCP:       op.After.TCP,
				Since:     now,
			}
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	routes := make([]state.Route, 0, len(keys))
	for _, key := range keys {
		routes = append(routes, byKey[key])
	}
	return routes
}

func serviceInfos(services []config.ResolvedService, dns string, healthByName map[string]health.Result, paused map[string]bool) []ServiceInfo {
	infos := make([]ServiceInfo, 0, len(services))
	for _, service := range services {
		info := ServiceInfo{
			Name:       service.Name,
			Target:     service.Target,
			Host:       service.Host,
			Port:       service.Port,
			HTTPSPort:  service.HTTPSPort,
			Path:       service.Path,
			Public:     service.Public,
			Paused:     paused[service.Name],
			URL:        routeURL(dns, reconcile.Route{HTTPSPort: service.HTTPSPort, Path: service.Path, TCP: service.TCP()}),
			Domain:     service.Domain,
			Health:     healthByName[service.Name],
			TCP:        service.TCP(),
			Cloudflare: service.Cloudflare,
		}
		if !service.OnTailscale() {
			info.URL = "" // its URL is the Cloudflare one
		}
		if service.Domain != "" && paused[service.Name] {
			info.LocalState = "paused"
		}
		if info.Health.Service == "" {
			info.Health.Service = service.Name
		}
		infos = append(infos, info)
	}
	return infos
}

func compactBoolMap(in map[string]bool) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for key, value := range in {
		if value {
			out[key] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func indexHealth(results []health.Result) map[string]health.Result {
	indexed := make(map[string]health.Result, len(results))
	for _, result := range results {
		indexed[result.Service] = result
	}
	return indexed
}

// localDomains lists the project's served names. Each gets a LAN port too,
// reused from saved state so a bookmark on a phone keeps working.
func localDomains(cfg config.Project, paused map[string]bool, saved []state.LocalDomain) []state.LocalDomain {
	ports := map[string]int{}
	taken := map[int]bool{}
	for _, domain := range saved {
		if domain.LANPort != 0 && !domain.TCP {
			ports[domain.Service] = domain.LANPort
			taken[domain.LANPort] = true
		}
	}
	domains := make([]state.LocalDomain, 0)
	for _, service := range cfg.Services {
		if service.Domain == "" || paused[service.Name] {
			continue
		}
		domain := state.LocalDomain{Service: service.Name, Name: service.Domain, Target: service.Target, TCP: service.TCP()}
		if domain.TCP {
			if cfg.Local.LAN {
				domain.LANPort = int(service.HTTPSPort) // db.local:5432 is the listen port itself
			}
		} else if cfg.Local.LAN {
			domain.LANPort = ports[service.Name]
			if domain.LANPort == 0 {
				domain.LANPort = nextLANPort(taken)
				taken[domain.LANPort] = true
			}
		}
		domains = append(domains, domain)
	}
	return domains
}

func sameDomains(left, right []state.LocalDomain) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (s *Service) rejectDomainConflicts(projectID string, domains []state.LocalDomain) error {
	lister, ok := s.store.(interface {
		List() ([]state.ProjectState, error)
	})
	if !ok || len(domains) == 0 {
		return nil
	}
	projects, err := lister.List()
	if err != nil {
		return fmt.Errorf("Pier could not check local domain names: %w", err)
	}
	claimed := map[string]string{}
	for _, project := range projects {
		if project.ProjectID == projectID {
			continue
		}
		for _, domain := range project.Domains {
			claimed[domain.Name] = project.ProjectID
		}
	}
	for _, domain := range domains {
		if claimed[domain.Name] != "" {
			return fmt.Errorf("Pier could not publish %s because another project already uses that name", domain.Name)
		}
	}
	return nil
}

// withLocal fills each service's local URL and state from the daemon report.
func withLocal(infos []ServiceInfo, report localname.Report) {
	for i := range infos {
		if infos[i].Domain == "" || infos[i].LocalState == "paused" {
			continue
		}
		infos[i].LocalState = report.State(infos[i].Domain)
		infos[i].LocalURL = report.URL(infos[i].Domain)
		infos[i].LANURL = report.LANURL(infos[i].Domain)
	}
}

func hasDomains(cfg config.Project) bool {
	for _, service := range cfg.Services {
		if service.Domain != "" {
			return true
		}
	}
	return false
}

func serviceURL(dnsName string, httpsPort uint16, path string) string {
	dnsName = strings.TrimSuffix(dnsName, ".")
	if dnsName == "" {
		return "" // Tailscale is not available, so there is no tailnet URL.
	}
	if path == "" {
		path = "/"
	}
	if httpsPort == 443 {
		return "https://" + dnsName + path
	}
	return "https://" + net.JoinHostPort(dnsName, strconv.Itoa(int(httpsPort))) + path
}

func copyBoolMap(in map[string]bool) map[string]bool {
	if len(in) == 0 {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func configHash(path string) string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type liveTailscale struct {
	client *tailscale.Client
	runner tailscale.Runner
	dns    string
}

func newLiveTailscale(runner tailscale.Runner) *liveTailscale {
	return &liveTailscale{client: tailscale.NewClient(runner), runner: runner}
}

func (l *liveTailscale) Check(ctx context.Context) (tailscale.Capabilities, error) {
	caps, err := l.client.Check(ctx)
	if err == nil {
		if st, statusErr := l.client.Status(ctx); statusErr == nil {
			l.dns = st.DNSName
		}
	}
	return caps, err
}

func (l *liveTailscale) Status(ctx context.Context) (tailscale.Status, error) {
	st, err := l.client.Status(ctx)
	if err == nil {
		l.dns = st.DNSName
	}
	return st, err
}

func (l *liveTailscale) DNSName() string { return l.dns }

func (l *liveTailscale) Routes(ctx context.Context) ([]reconcile.Route, error) {
	stdout, stderr, err := l.runner.Run(ctx, "tailscale", "serve", "status", "--json")
	if err != nil {
		if len(stderr) > 0 {
			return nil, fmt.Errorf("Pier could not read Tailscale routes: %s", strings.TrimSpace(string(stderr)))
		}
		return nil, fmt.Errorf("Pier could not read Tailscale routes: %w", err)
	}
	parsed, err := tailscale.ParseStatus(stdout)
	if err != nil {
		return nil, fmt.Errorf("Pier could not parse Tailscale routes: %w", err)
	}
	if l.dns == "" {
		l.dns = parsed.DNSName
	}
	return convertRoutes(parsed.Routes), nil
}

func (l *liveTailscale) Apply(ctx context.Context, op reconcile.Operation) error {
	var (
		args []string
		err  error
	)
	switch op.Kind {
	case reconcile.KindDelete:
		args, err = tailscale.DownArgs(toTailscaleRoute(op.Before))
	case reconcile.KindCreate, reconcile.KindUpdate:
		args, err = tailscale.UpArgs(toTailscaleRoute(op.After))
	default:
		return nil
	}
	if err != nil {
		return err
	}
	_, stderr, err := l.runner.Run(ctx, "tailscale", args...)
	if err != nil {
		if len(stderr) > 0 {
			return fmt.Errorf("Pier could not apply Tailscale route changes: %s", strings.TrimSpace(string(stderr)))
		}
		return fmt.Errorf("Pier could not apply Tailscale route changes: %w", err)
	}
	return nil
}

func convertRoutes(routes []tailscale.Route) []reconcile.Route {
	out := make([]reconcile.Route, 0, len(routes))
	for _, route := range routes {
		out = append(out, reconcile.Route{
			HTTPSPort: route.HTTPSPort,
			Path:      route.Path,
			Target:    route.Target,
			Public:    route.Public,
			TCP:       route.TCP,
		})
	}
	return out
}

func toTailscaleRoute(route reconcile.Route) tailscale.Route {
	return tailscale.Route{
		HTTPSPort: route.HTTPSPort,
		Path:      route.Path,
		Target:    route.Target,
		Public:    route.Public,
		TCP:       route.TCP,
	}
}
