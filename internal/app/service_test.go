package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"pier/internal/config"
	"pier/internal/health"
	"pier/internal/project"
	"pier/internal/reconcile"
	"pier/internal/state"
	"pier/internal/tailscale"
)

func TestUpSequence(t *testing.T) {
	env := newEnv()
	env.after = env.desiredRoutes()
	svc := env.service()

	result, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	wantCalls := []string{
		"find project",
		"load and validate config",
		"check Tailscale prerequisites",
		"read actual routes",
		"load owned state and overrides",
		"build plan",
		"reject conflicts unless forced",
		"check local targets",
		"apply operations",
		"verify actual routes",
		"save owned state",
	}
	if !reflect.DeepEqual(env.calls, wantCalls) {
		t.Errorf("Up() sequence = %#v, want %#v", env.calls, wantCalls)
	}

	wantURLs := map[string]string{
		"api":     "https://host.ts.net:8443/api",
		"web":     "https://host.ts.net:8443/",
		"webhook": "https://host.ts.net/hooks",
	}
	if len(result.Services) != 3 {
		t.Fatalf("Up() services = %d, want 3", len(result.Services))
	}
	for _, info := range result.Services {
		if url := wantURLs[info.Name]; info.URL != url {
			t.Errorf("Up() %s URL = %q, want %q", info.Name, info.URL, url)
		}
		if info.Health.Status != health.StatusHealthy {
			t.Errorf("Up() %s health = %q, want %q", info.Name, info.Health.Status, health.StatusHealthy)
		}
	}
	if env.saved == nil || len(env.saved.Routes) != 3 {
		t.Errorf("Up() saved routes = %#v, want 3 owned routes", env.saved)
	}
}

func TestPlanDoesNotMutate(t *testing.T) {
	env := newEnv()
	svc := env.service()

	result, err := svc.Plan(context.Background(), PlanRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if env.mutated {
		t.Error("Plan() invoked a Tailscale mutation")
	}
	for _, call := range env.calls {
		if call == "apply operations" || call == "save owned state" || call == "check local targets" {
			t.Errorf("Plan() call %q is a mutating or apply-path step", call)
		}
	}

	want := reconcile.Build(env.desiredRoutes(), nil, nil, false)
	if !reflect.DeepEqual(result.Plan, want) {
		t.Errorf("Plan() = %#v, want %#v from reconcile.Build", result.Plan, want)
	}
}

func TestDownDeletesOnlyOwnedRoutes(t *testing.T) {
	env := newEnv()
	owned := reconcile.Route{
		Service:   "web",
		ProjectID: env.project.ID,
		HTTPSPort: 8443,
		Path:      "/",
		Target:    "http://127.0.0.1:3000",
	}
	unmanaged := reconcile.Route{
		Service:   "other",
		HTTPSPort: 8443,
		Path:      "/other",
		Target:    "http://127.0.0.1:9000",
	}
	env.actual = []reconcile.Route{owned, unmanaged}
	env.state.Routes = []state.Route{{Service: "web", HTTPSPort: 8443, Path: "/"}}
	env.after = []reconcile.Route{unmanaged}
	svc := env.service()

	result, err := svc.Down(context.Background(), DownRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Down() error = %v", err)
	}
	if !env.mutated {
		t.Fatal("Down() did not apply owned-route deletion")
	}
	if len(env.applyCalls) != 1 || env.applyCalls[0].Kind != reconcile.KindDelete {
		t.Fatalf("Down() mutations = %#v, want a single delete", env.applyCalls)
	}
	if env.applyCalls[0].Before.Path != "/" || env.applyCalls[0].Before.HTTPSPort != 8443 {
		t.Errorf("Down() deleted %#v, want the owned web route", env.applyCalls[0].Before)
	}
	want := reconcile.Build(nil, env.actual, []reconcile.Route{{
		Service:   "web",
		ProjectID: env.project.ID,
		HTTPSPort: 8443,
		Path:      "/",
	}}, false)
	if !reflect.DeepEqual(result.Plan.Operations, want.Operations) {
		t.Errorf("Down() plan = %#v, want owned-route deletion %#v", result.Plan.Operations, want.Operations)
	}
}

func TestUnchangedUpDoesNotMutate(t *testing.T) {
	env := newEnv()
	env.actual = env.desiredRoutes()
	env.state.Routes = []state.Route{
		{Service: "api", HTTPSPort: 8443, Path: "/api"},
		{Service: "web", HTTPSPort: 8443, Path: "/"},
		{Service: "webhook", HTTPSPort: 443, Path: "/hooks"},
	}
	svc := env.service()

	_, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if env.mutated || len(env.applyCalls) > 0 {
		t.Errorf("unchanged Up() mutations = %#v, want none", env.applyCalls)
	}
	if env.saved != nil {
		t.Errorf("unchanged Up() saved state = %#v, want no persist", env.saved)
	}
}

func TestUpAndPlanUseTheSameBuild(t *testing.T) {
	env := newEnv()
	env.after = env.desiredRoutes()
	var built []reconcile.Plan
	svc := env.service()
	original := svc.build
	svc.build = func(desired, actual, owned []reconcile.Route, force bool) reconcile.Plan {
		plan := original(desired, actual, owned, force)
		built = append(built, plan)
		return plan
	}

	planned, err := svc.Plan(context.Background(), PlanRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	up, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if len(built) != 2 {
		t.Fatalf("build calls = %d, want 2", len(built))
	}
	if !reflect.DeepEqual(planned.Plan, up.Plan) || !reflect.DeepEqual(planned.Plan, built[0]) {
		t.Errorf("Plan/Up plans differ: plan=%#v up=%#v", planned.Plan, up.Plan)
	}
}

func TestUpRejectsConflictsUnlessForced(t *testing.T) {
	env := newEnv()
	env.actual = []reconcile.Route{{
		HTTPSPort: 8443,
		Path:      "/",
		Target:    "http://127.0.0.1:9999",
	}}
	svc := env.service()

	_, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root})
	var conflictErr *ConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("Up() error = %v, want *ConflictError", err)
	}
	if !strings.HasPrefix(err.Error(), "Pier ") {
		t.Errorf("Up() conflict error = %q, want a Pier explanation", err)
	}
	if env.mutated {
		t.Error("Up() applied mutations despite an unmanaged conflict")
	}

	env.calls = nil
	env.routeReads = 0
	env.applyRecorded = false
	env.healthRecorded = false
	env.after = env.desiredRoutes()
	_, err = svc.Up(context.Background(), UpRequest{Start: env.project.Root, Force: true})
	if err != nil {
		t.Fatalf("Up(force) error = %v", err)
	}
	if !env.mutated {
		t.Error("Up(force) did not take over the conflicting route")
	}
}

func TestUpStrictUnavailableTargetIsPreApplyError(t *testing.T) {
	env := newEnv()
	env.health["web"] = health.Result{Service: "web", Status: health.StatusUnavailable, Error: "connection refused"}
	svc := env.service()

	_, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root, Strict: true})
	var unavailable *UnavailableTargetsError
	if !errors.As(err, &unavailable) {
		t.Fatalf("Up(strict) error = %v, want *UnavailableTargetsError", err)
	}
	if !strings.HasPrefix(err.Error(), "Pier ") {
		t.Errorf("Up(strict) error = %q, want a Pier explanation", err)
	}
	if env.mutated {
		t.Error("Up(strict) applied mutations after a health failure")
	}
}

func TestUpHealthFailureIsDataUnlessStrict(t *testing.T) {
	env := newEnv()
	env.health["webhook"] = health.Result{Service: "webhook", Status: health.StatusUnavailable, Error: "connection refused"}
	env.after = env.desiredRoutes()
	svc := env.service()

	result, err := svc.Up(context.Background(), UpRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Up() error = %v, want health data rather than a fatal error", err)
	}
	if !env.mutated {
		t.Error("Up() skipped apply after a non-strict health failure")
	}
	found := false
	for _, info := range result.Services {
		if info.Name == "webhook" {
			found = true
			if info.Health.Status != health.StatusUnavailable {
				t.Errorf("webhook health = %q, want %q", info.Health.Status, health.StatusUnavailable)
			}
		}
	}
	if !found {
		t.Fatal("Up() result missing webhook health")
	}
}

func TestValidateReturnsAggregatedErrors(t *testing.T) {
	env := newEnv()
	env.raw.Version = 2
	env.raw.Name = ""
	env.raw.Services = nil
	svc := env.service()

	result, err := svc.Validate(context.Background(), ValidateRequest{Start: env.project.Root})
	var invalid *InvalidConfigError
	if !errors.As(err, &invalid) {
		t.Fatalf("Validate() error = %v, want *InvalidConfigError", err)
	}
	if !strings.HasPrefix(err.Error(), "Pier ") {
		t.Errorf("Validate() error = %q, want a Pier explanation", err)
	}
	if len(result.Errors) < 2 {
		t.Fatalf("Validate() errors = %#v, want aggregated failures", result.Errors)
	}
	if env.mutated {
		t.Error("Validate() invoked a Tailscale mutation")
	}
}

func TestStatusReturnsHealthAndURLs(t *testing.T) {
	env := newEnv()
	env.health["api"] = health.Result{Service: "api", Status: health.StatusUnavailable, Error: "connection refused"}
	svc := env.service()

	result, err := svc.Status(context.Background(), StatusRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if env.mutated {
		t.Error("Status() invoked a Tailscale mutation")
	}
	byName := map[string]ServiceInfo{}
	for _, info := range result.Services {
		byName[info.Name] = info
	}
	if byName["web"].URL != "https://host.ts.net:8443/" {
		t.Errorf("web URL = %q", byName["web"].URL)
	}
	if byName["webhook"].URL != "https://host.ts.net/hooks" {
		t.Errorf("webhook URL = %q", byName["webhook"].URL)
	}
	if byName["web"].Health.Status != health.StatusHealthy {
		t.Errorf("web health = %q, want healthy", byName["web"].Health.Status)
	}
	if byName["api"].Health.Status != health.StatusUnavailable {
		t.Errorf("api health = %q, want unavailable", byName["api"].Health.Status)
	}
}

func TestStatusReturnsStateLoadError(t *testing.T) {
	env := newEnv()
	env.loadStateErr = errors.New("decode project state")
	svc := env.service()

	result, err := svc.Status(context.Background(), StatusRequest{Start: env.project.Root})
	if err == nil || !strings.HasPrefix(err.Error(), "Pier could not load project state:") {
		t.Fatalf("Status() error = %v, want Pier state explanation", err)
	}
	if result.Project.ID != env.project.ID {
		t.Errorf("Status() project = %#v, want %#v", result.Project, env.project)
	}
	if env.mutated {
		t.Error("Status() invoked a Tailscale mutation")
	}
}

func TestDoctorReportsTailscaleDiagnostics(t *testing.T) {
	env := newEnv()
	env.checkErr = errors.New("Tailscale is not installed or is not available on PATH")
	env.caps = tailscale.Capabilities{Installed: false}
	svc := env.service()

	result, err := svc.Doctor(context.Background(), DoctorRequest{Start: env.project.Root})
	if err != nil {
		t.Fatalf("Doctor() error = %v, want diagnostics as data", err)
	}
	if result.Capabilities != env.caps {
		t.Errorf("Doctor() capabilities = %#v, want %#v", result.Capabilities, env.caps)
	}
	if result.TailscaleErr == nil {
		t.Error("Doctor() TailscaleErr = nil, want the Check failure")
	}
	if env.mutated {
		t.Error("Doctor() invoked a Tailscale mutation")
	}
}

func TestShareUnknownService(t *testing.T) {
	env := newEnv()
	svc := env.service()

	_, err := svc.Share(context.Background(), ShareRequest{Start: env.project.Root, Service: "missing"})
	var notFound *ServiceNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Share() error = %v, want *ServiceNotFoundError", err)
	}
	if !strings.Contains(err.Error(), "Pier") {
		t.Errorf("Share() error = %q, want a Pier explanation", err)
	}
	if env.mutated {
		t.Error("Share() mutated Tailscale for an unknown service")
	}
}

func TestUnshareUnknownService(t *testing.T) {
	env := newEnv()
	svc := env.service()

	_, err := svc.Unshare(context.Background(), UnshareRequest{Start: env.project.Root, Service: "missing"})
	var notFound *ServiceNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Unshare() error = %v, want *ServiceNotFoundError", err)
	}
}

func TestShareUsesReconcileApplyPath(t *testing.T) {
	env := newEnv()
	env.actual = env.desiredRoutes()
	env.state.Routes = []state.Route{
		{Service: "api", HTTPSPort: 8443, Path: "/api"},
		{Service: "web", HTTPSPort: 8443, Path: "/"},
		{Service: "webhook", HTTPSPort: 443, Path: "/hooks"},
	}
	env.after = []reconcile.Route{
		{Service: "api", ProjectID: env.project.ID, HTTPSPort: 443, Path: "/api", Target: "http://127.0.0.1:4000", Public: true},
		{Service: "web", ProjectID: env.project.ID, HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"},
		{Service: "webhook", ProjectID: env.project.ID, HTTPSPort: 443, Path: "/hooks", Target: "http://127.0.0.1:8787", Public: true},
	}
	svc := env.service()

	result, err := svc.Share(context.Background(), ShareRequest{Start: env.project.Root, Service: "api"})
	if err != nil {
		t.Fatalf("Share() error = %v", err)
	}
	if !env.built {
		t.Error("Share() did not call reconcile.Build")
	}
	if !env.mutated {
		t.Error("Share() did not apply through the reconciler")
	}
	if result.Service.URL != "https://host.ts.net/api" {
		t.Errorf("Share() URL = %q, want public URL", result.Service.URL)
	}
}

func TestServiceURL(t *testing.T) {
	tests := []struct {
		dns  string
		port uint16
		path string
		want string
	}{
		{dns: "host.ts.net", port: 443, path: "/hooks", want: "https://host.ts.net/hooks"},
		{dns: "host.ts.net.", port: 8443, path: "/", want: "https://host.ts.net:8443/"},
		{dns: "host.ts.net", port: 10000, path: "/x", want: "https://host.ts.net:10000/x"},
	}
	for _, tt := range tests {
		if got := serviceURL(tt.dns, tt.port, tt.path); got != tt.want {
			t.Errorf("serviceURL(%q, %d, %q) = %q, want %q", tt.dns, tt.port, tt.path, got, tt.want)
		}
	}
}

type fakeEnv struct {
	calls   []string
	project project.Context
	raw     config.Config
	caps    tailscale.Capabilities
	dnsName string
	status  tailscale.Status
	actual  []reconcile.Route
	after   []reconcile.Route
	state   state.ProjectState
	health  map[string]health.Result
	now     time.Time

	findErr      error
	loadErr      error
	checkErr     error
	routesErr    error
	applyErr     error
	loadStateErr error
	saveErr      error

	applyCalls     []reconcile.Operation
	mutated        bool
	built          bool
	saved          *state.ProjectState
	healthRecorded bool
	applyRecorded  bool
	routeReads     int
}

func newEnv() *fakeEnv {
	public := true
	return &fakeEnv{
		project: project.Context{
			Root:       "/tmp/greppa",
			ConfigPath: "/tmp/greppa/pier.yaml",
			ID:         "019f6429-aaaa-4bbb-8ccc-ddddeeeeffff",
		},
		raw: config.Config{
			Version: 1,
			Name:    "greppa",
			Services: map[string]config.Service{
				"web":     {Target: "localhost:3000"},
				"api":     {Target: "localhost:4000", Path: "/api"},
				"webhook": {Target: "localhost:8787", Path: "/hooks", Public: &public},
			},
		},
		caps: tailscale.Capabilities{
			Installed: true, DaemonRunning: true, Authenticated: true,
			MagicDNS: true, HTTPS: true, Funnel: true,
		},
		dnsName: "host.ts.net.",
		status:  tailscale.Status{DNSName: "host.ts.net."},
		health:  map[string]health.Result{},
		now:     time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC),
		state:   state.ProjectState{Version: state.CurrentVersion, ProjectID: "019f6429-aaaa-4bbb-8ccc-ddddeeeeffff"},
	}
}

func (e *fakeEnv) record(step string) {
	e.calls = append(e.calls, step)
}

func (e *fakeEnv) desiredRoutes() []reconcile.Route {
	normalized, err := config.Normalize(e.raw)
	if err != nil {
		panic(err)
	}
	return desiredRoutes(e.project, normalized, e.state.Overrides, e.state.Paused)
}

func (e *fakeEnv) service() *Service {
	return &Service{
		find:   e.Find,
		load:   e.LoadConfig,
		store:  e,
		ts:     e,
		health: e.CheckHealth,
		build: func(desired, actual, owned []reconcile.Route, force bool) reconcile.Plan {
			e.record("build plan")
			e.built = true
			return reconcile.Build(desired, actual, owned, force)
		},
		afterPlan: func(plan reconcile.Plan, force bool) error {
			e.record("reject conflicts unless forced")
			return rejectConflicts(plan, force)
		},
		now: func() time.Time { return e.now },
	}
}

func (e *fakeEnv) Find(string) (project.Context, error) {
	e.record("find project")
	if e.findErr != nil {
		return project.Context{}, e.findErr
	}
	return e.project, nil
}

func (e *fakeEnv) LoadConfig(string) (config.Config, error) {
	e.record("load and validate config")
	if e.loadErr != nil {
		return config.Config{}, e.loadErr
	}
	return e.raw, nil
}

func (e *fakeEnv) Load(id string) (state.ProjectState, error) {
	e.record("load owned state and overrides")
	if e.loadStateErr != nil {
		return state.ProjectState{}, e.loadStateErr
	}
	st := e.state
	if st.ProjectID == "" {
		st.ProjectID = id
	}
	return st, nil
}

func (e *fakeEnv) Save(st state.ProjectState) error {
	e.record("save owned state")
	saved := st
	e.saved = &saved
	return e.saveErr
}

func (e *fakeEnv) Delete(string) error { return nil }

func (e *fakeEnv) Check(context.Context) (tailscale.Capabilities, error) {
	e.record("check Tailscale prerequisites")
	return e.caps, e.checkErr
}

func (e *fakeEnv) Status(context.Context) (tailscale.Status, error) {
	st := e.status
	if st.DNSName == "" {
		st.DNSName = e.dnsName
	}
	return st, nil
}

func (e *fakeEnv) DNSName() string {
	return e.dnsName
}

func (e *fakeEnv) Routes(context.Context) ([]reconcile.Route, error) {
	e.routeReads++
	if e.applyRecorded {
		e.record("verify actual routes")
		if e.after != nil {
			return e.after, nil
		}
		return e.actual, nil
	}
	e.record("read actual routes")
	if e.routesErr != nil {
		return nil, e.routesErr
	}
	return e.actual, nil
}

func (e *fakeEnv) Apply(_ context.Context, op reconcile.Operation) error {
	if !e.applyRecorded {
		e.record("apply operations")
		e.applyRecorded = true
	}
	e.applyCalls = append(e.applyCalls, op)
	e.mutated = true
	return e.applyErr
}

func (e *fakeEnv) CheckHealth(_ context.Context, service config.ResolvedService) health.Result {
	if !e.healthRecorded {
		e.record("check local targets")
		e.healthRecorded = true
	}
	if result, ok := e.health[service.Name]; ok {
		return result
	}
	return health.Result{Service: service.Name, Status: health.StatusHealthy}
}
