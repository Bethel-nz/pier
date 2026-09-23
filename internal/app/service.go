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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Bethel-nz/pier/internal/config"
	"github.com/Bethel-nz/pier/internal/health"
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
}

// DownRequest removes routes owned by the current project.
type DownRequest struct {
	Start string
}

// DownResult contains the deletion plan for owned routes.
type DownResult struct {
	Project project.Context
	Plan    reconcile.Plan
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
	Health    health.Result
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
	sess.plan = s.planner(desiredRoutes(sess.project, sess.cfg, sess.state.Overrides, sess.state.Paused), sess.actual, ownedRoutes(sess.project, sess.state), req.Force)
	result.Plan = sess.plan
	return result, s.reviewPlan(sess.plan, req.Force)
}

// Up validates, plans, applies, verifies, and persists owned routes.
func (s *Service) Up(ctx context.Context, req UpRequest) (UpResult, error) {
	return s.reconcile(ctx, req.Start, req.Force, req.Strict, nil, "")
}

// Down deletes only routes recorded as owned by this project.
func (s *Service) Down(ctx context.Context, req DownRequest) (DownResult, error) {
	sess, err := s.prepare(ctx, req.Start)
	result := DownResult{Project: sess.project}
	if err != nil {
		return result, err
	}
	sess.plan = s.planner(nil, sess.actual, ownedRoutes(sess.project, sess.state), false)
	result.Plan = sess.plan
	if err := s.reviewPlan(sess.plan, false); err != nil {
		return result, err
	}
	if err := s.applyAndPersist(ctx, &sess, sess.state.Overrides, sess.state.Paused); err != nil {
		return result, err
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
	dns := s.lookupDNS(ctx, st.DNSName)
	services := effectiveServices(loaded.cfg, st.Overrides)
	results := s.checkTargets(ctx, services)
	return StatusResult{
		Project:  loaded.project,
		DNSName:  dns,
		Services: serviceInfos(services, dns, indexHealth(results), st.Paused),
	}, nil
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
	return result, loadErr
}

// Share exposes a service through Funnel using the normal reconcile path.
func (s *Service) Share(ctx context.Context, req ShareRequest) (ShareResult, error) {
	up, err := s.reconcile(ctx, req.Start, req.Force, req.Strict, func(overrides, _ map[string]bool) error {
		overrides[req.Service] = true
		return nil
	}, req.Service)
	return ShareResult{Project: up.Project, Plan: up.Plan, Service: lookupService(up.Services, req.Service)}, err
}

// Unshare restores the configured public value using the normal reconcile path.
func (s *Service) Unshare(ctx context.Context, req UnshareRequest) (UnshareResult, error) {
	up, err := s.reconcile(ctx, req.Start, req.Force, req.Strict, func(overrides, _ map[string]bool) error {
		delete(overrides, req.Service)
		return nil
	}, req.Service)
	return UnshareResult{Project: up.Project, Plan: up.Plan, Service: lookupService(up.Services, req.Service)}, err
}

// Pause removes a service route from Tailscale and leaves the local process running.
func (s *Service) Pause(ctx context.Context, req PauseRequest) (PauseResult, error) {
	up, err := s.reconcile(ctx, req.Start, req.Force, req.Strict, func(_, paused map[string]bool) error {
		paused[req.Service] = true
		return nil
	}, req.Service)
	return PauseResult{Project: up.Project, Plan: up.Plan, Service: lookupService(up.Services, req.Service)}, err
}

// Resume restores a paused service route through the normal reconcile path.
func (s *Service) Resume(ctx context.Context, req ResumeRequest) (ResumeResult, error) {
	up, err := s.reconcile(ctx, req.Start, req.Force, req.Strict, func(_, paused map[string]bool) error {
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
	public := req.Public
	service.Public = &public
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
}

// OpenResult is the resolved service URL.
type OpenResult struct {
	URL string
}

// CopyRequest looks up a service URL for the clipboard.
type CopyRequest struct {
	Start   string
	Service string
}

// CopyResult is the resolved service URL after a successful copy.
type CopyResult struct {
	URL string
}

// Open resolves the current URL for a named service and opens it.
func (s *Service) Open(ctx context.Context, req OpenRequest) (OpenResult, error) {
	url, err := s.lookupConfiguredURL(ctx, req.Start, req.Service)
	if err != nil {
		return OpenResult{}, err
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
	url, err := s.lookupConfiguredURL(ctx, req.Start, req.Service)
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

func (s *Service) lookupConfiguredURL(ctx context.Context, start, name string) (string, error) {
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
	actual, err := s.ts.Routes(ctx)
	if err != nil {
		return "", fmt.Errorf("Pier could not read Tailscale routes: %w", err)
	}
	key := reconcile.Route{HTTPSPort: found.HTTPSPort, Path: found.Path}.Key()
	for _, route := range actual {
		if route.Key() == key {
			return found.URL, nil
		}
	}
	return "", &NotConfiguredError{Name: name}
}

func (s *Service) reconcile(ctx context.Context, start string, force, strict bool, mutate func(overrides, paused map[string]bool) error, focus string) (UpResult, error) {
	sess, err := s.prepare(ctx, start)
	result := UpResult{Project: sess.project}
	if err != nil {
		return result, err
	}
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
	sess.plan = s.planner(desiredRoutes(sess.project, sess.cfg, overrides, paused), sess.actual, ownedRoutes(sess.project, sess.state), force)
	result.Plan = sess.plan
	if err := s.reviewPlan(sess.plan, force); err != nil {
		return result, err
	}
	healthResults := s.checkTargets(ctx, services)
	result.Services = serviceInfos(services, sess.dns, indexHealth(healthResults), paused)
	if strict {
		if err := strictHealth(healthResults); err != nil {
			return result, err
		}
	}
	if err := s.applyAndPersist(ctx, &sess, overrides, paused); err != nil {
		return result, err
	}
	return result, nil
}

type session struct {
	project project.Context
	cfg     config.Project
	state   state.ProjectState
	actual  []reconcile.Route
	dns     string
	plan    reconcile.Plan
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
		return sess, &PrerequisiteError{Err: err}
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

func (s *Service) applyAndPersist(ctx context.Context, sess *session, overrides, paused map[string]bool) error {
	result, err := reconcile.Apply(ctx, sess.plan, s.ts)
	if err != nil {
		return &ApplyError{Err: err, Result: result}
	}
	pausedChanged := !maps.Equal(compactBoolMap(sess.state.Paused), compactBoolMap(paused))
	if result.Verified == nil && !pausedChanged {
		return nil
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
		st.Routes = nextOwned(st.Routes, result.Completed)
		st.Overrides = overrides
	}
	st.Paused = compactBoolMap(paused)
	if s.now != nil {
		st.UpdatedAt = s.now()
	} else {
		st.UpdatedAt = time.Now()
	}
	if err := s.store.Save(st); err != nil {
		return fmt.Errorf("Pier could not save project state: %w", err)
	}
	sess.state = st
	return nil
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
		if !ok {
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

func desiredRoutes(proj project.Context, cfg config.Project, overrides, paused map[string]bool) []reconcile.Route {
	services := effectiveServices(cfg, overrides)
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
		routes = append(routes, reconcile.Route{
			Service:   service.Name,
			ProjectID: proj.ID,
			HTTPSPort: service.HTTPSPort,
			Path:      service.Path,
			Target:    service.Target,
			Public:    service.Public,
		})
	}
	return routes
}

func ownedRoutes(proj project.Context, st state.ProjectState) []reconcile.Route {
	routes := make([]reconcile.Route, 0, len(st.Routes))
	for _, route := range st.Routes {
		routes = append(routes, reconcile.Route{
			Service:   route.Service,
			ProjectID: proj.ID,
			HTTPSPort: route.HTTPSPort,
			Path:      route.Path,
		})
	}
	return routes
}

func nextOwned(prev []state.Route, completed []reconcile.Operation) []state.Route {
	byKey := make(map[string]state.Route, len(prev)+len(completed))
	for _, route := range prev {
		byKey[reconcile.Route{HTTPSPort: route.HTTPSPort, Path: route.Path}.Key()] = route
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
			Name:      service.Name,
			Target:    service.Target,
			Host:      service.Host,
			Port:      service.Port,
			HTTPSPort: service.HTTPSPort,
			Path:      service.Path,
			Public:    service.Public,
			Paused:    paused[service.Name],
			URL:       serviceURL(dns, service.HTTPSPort, service.Path),
			Health:    healthByName[service.Name],
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

func serviceURL(dnsName string, httpsPort uint16, path string) string {
	dnsName = strings.TrimSuffix(dnsName, ".")
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
	}
}
