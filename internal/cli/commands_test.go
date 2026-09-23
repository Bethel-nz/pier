package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/config"
	"github.com/Bethel-nz/pier/internal/health"
	"github.com/Bethel-nz/pier/internal/project"
	"github.com/Bethel-nz/pier/internal/reconcile"
	"github.com/Bethel-nz/pier/internal/tailscale"
)

type fakeApp struct {
	validate    app.ValidateResult
	validateErr error
	plan        app.PlanResult
	planErr     error
	up          app.UpResult
	upErr       error
	upReq       app.UpRequest
	down        app.DownResult
	downErr     error
	status      app.StatusResult
	statusErr   error
	doctor      app.DoctorResult
	doctorErr   error
	shareErr    error
	unshareErr  error
	pauseErr    error
	resumeErr   error
	addErr      error
	addReq      app.AddServiceRequest
	openErr     error
	copyErr     error
}

func (f *fakeApp) Validate(context.Context, app.ValidateRequest) (app.ValidateResult, error) {
	return f.validate, f.validateErr
}
func (f *fakeApp) Plan(context.Context, app.PlanRequest) (app.PlanResult, error) {
	return f.plan, f.planErr
}
func (f *fakeApp) Up(_ context.Context, req app.UpRequest) (app.UpResult, error) {
	f.upReq = req
	return f.up, f.upErr
}
func (f *fakeApp) Down(context.Context, app.DownRequest) (app.DownResult, error) {
	return f.down, f.downErr
}
func (f *fakeApp) Status(context.Context, app.StatusRequest) (app.StatusResult, error) {
	return f.status, f.statusErr
}
func (f *fakeApp) Doctor(context.Context, app.DoctorRequest) (app.DoctorResult, error) {
	return f.doctor, f.doctorErr
}
func (f *fakeApp) Share(context.Context, app.ShareRequest) (app.ShareResult, error) {
	return app.ShareResult{}, f.shareErr
}
func (f *fakeApp) Unshare(context.Context, app.UnshareRequest) (app.UnshareResult, error) {
	return app.UnshareResult{}, f.unshareErr
}
func (f *fakeApp) Pause(context.Context, app.PauseRequest) (app.PauseResult, error) {
	return app.PauseResult{}, f.pauseErr
}
func (f *fakeApp) Resume(context.Context, app.ResumeRequest) (app.ResumeResult, error) {
	return app.ResumeResult{}, f.resumeErr
}
func (f *fakeApp) AddService(_ context.Context, req app.AddServiceRequest) (app.AddServiceResult, error) {
	f.addReq = req
	return app.AddServiceResult{}, f.addErr
}
func (f *fakeApp) Open(context.Context, app.OpenRequest) (app.OpenResult, error) {
	return app.OpenResult{}, f.openErr
}
func (f *fakeApp) Copy(context.Context, app.CopyRequest) (app.CopyResult, error) {
	return app.CopyResult{}, f.copyErr
}

func runCLI(t *testing.T, application App, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := ExecuteWith(context.Background(), args, &stdout, &stderr, application)
	return stdout.String(), stderr.String(), err
}

func TestValidateSuccessAndFailure(t *testing.T) {
	stdout, _, err := runCLI(t, &fakeApp{validate: app.ValidateResult{Project: project.Context{ID: "p"}}}, "validate")
	if err != nil {
		t.Fatalf("validate success error = %v", err)
	}
	if !strings.Contains(stdout, "valid") {
		t.Fatalf("validate success output = %q", stdout)
	}

	invalid := &app.InvalidConfigError{Errors: []config.ValidationError{{Field: "name", Message: "must not be empty"}}}
	_, stderr, err := runCLI(t, &fakeApp{validateErr: invalid, validate: app.ValidateResult{Errors: invalid.Errors}}, "validate")
	if !errors.As(err, new(*app.InvalidConfigError)) {
		t.Fatalf("validate failure error = %v", err)
	}
	if !strings.Contains(stderr, "Pier configuration is invalid") || !strings.Contains(stderr, "name: must not be empty") {
		t.Fatalf("validate failure stderr = %q", stderr)
	}
}

func TestPlanOperations(t *testing.T) {
	fake := &fakeApp{plan: app.PlanResult{
		Project: project.Context{ID: "p"},
		Plan: reconcile.Plan{Operations: []reconcile.Operation{
			{Kind: reconcile.KindCreate, After: reconcile.Route{Service: "web", HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000"}},
			{Kind: reconcile.KindUpdate, After: reconcile.Route{Service: "api", HTTPSPort: 8443, Path: "/api", Target: "http://127.0.0.1:4000"}},
			{Kind: reconcile.KindDelete, Before: reconcile.Route{Service: "old", HTTPSPort: 8443, Path: "/old", Target: "http://127.0.0.1:9"}},
			{Kind: reconcile.KindKeep, After: reconcile.Route{Service: "hook", HTTPSPort: 443, Path: "/hooks", Public: true, Target: "http://127.0.0.1:8787"}},
		}},
	}}
	stdout, _, err := runCLI(t, fake, "plan")
	if err != nil {
		t.Fatalf("plan error = %v", err)
	}
	for _, kind := range []string{"create", "update", "delete", "keep"} {
		if !strings.Contains(stdout, kind) {
			t.Errorf("plan missing %s in %q", kind, stdout)
		}
	}
}

func TestUpFlagsAndIdempotentOutput(t *testing.T) {
	fake := &fakeApp{up: app.UpResult{
		Project: project.Context{ID: "p"},
		Plan:    reconcile.Plan{Operations: []reconcile.Operation{{Kind: reconcile.KindKeep, After: reconcile.Route{Path: "/"}}}},
		Services: []app.ServiceInfo{
			{Name: "web", Host: "localhost", Port: 3000, Path: "/", Health: health.Result{Status: health.StatusHealthy}, URL: "https://host.ts.net:8443/"},
		},
	}}
	stdout, _, err := runCLI(t, fake, "up", "--strict", "--force")
	if err != nil {
		t.Fatalf("up error = %v", err)
	}
	if !fake.upReq.Force || !fake.upReq.Strict {
		t.Fatalf("up flags = %+v", fake.upReq)
	}
	if !strings.Contains(stdout, "already up") {
		t.Fatalf("idempotent up output = %q", stdout)
	}

	fake.upErr = &app.UnavailableTargetsError{Results: []health.Result{{Service: "web", Status: health.StatusUnavailable}}}
	_, stderr, err := runCLI(t, fake, "up", "--strict")
	if !errors.As(err, new(*app.UnavailableTargetsError)) {
		t.Fatalf("up --strict error = %v", err)
	}
	if !strings.Contains(stderr, "unavailable") {
		t.Fatalf("up --strict stderr = %q", stderr)
	}
}

func TestDownWithNoOwnedRoutes(t *testing.T) {
	stdout, _, err := runCLI(t, &fakeApp{down: app.DownResult{Project: project.Context{ID: "p"}}}, "down")
	if err != nil {
		t.Fatalf("down error = %v", err)
	}
	if !strings.Contains(stdout, "no owned routes") {
		t.Fatalf("down output = %q", stdout)
	}
}

func TestStatusHealthyAndUnavailable(t *testing.T) {
	stdout, _, err := runCLI(t, &fakeApp{status: app.StatusResult{Services: []app.ServiceInfo{
		{Name: "web", Host: "localhost", Port: 3000, Path: "/", Health: health.Result{Status: health.StatusHealthy}, URL: "https://host.ts.net:8443/"},
		{Name: "webhook", Host: "localhost", Port: 8787, Path: "/hooks", Public: true, Health: health.Result{Status: health.StatusUnavailable}, URL: "https://host.ts.net/hooks"},
	}}}, "status")
	if err != nil {
		t.Fatalf("status error = %v", err)
	}
	if !strings.Contains(stdout, "healthy") || !strings.Contains(stdout, "unavailable") {
		t.Fatalf("status output = %q", stdout)
	}
}

func TestDoctorDiagnostics(t *testing.T) {
	cases := []struct {
		name   string
		doctor app.DoctorResult
		want   string
	}{
		{"missing binary", app.DoctorResult{TailscaleErr: errors.New("Pier could not find the tailscale executable")}, "tailscale executable"},
		{"stopped daemon", app.DoctorResult{Capabilities: tailscale.Capabilities{Installed: true}, TailscaleErr: errors.New("Pier cannot reach the Tailscale daemon")}, "daemon"},
		{"signed-out", app.DoctorResult{Capabilities: tailscale.Capabilities{Installed: true, DaemonRunning: true}, TailscaleErr: errors.New("Pier is not signed in to Tailscale")}, "signed in"},
		{"unavailable Funnel", app.DoctorResult{Capabilities: tailscale.Capabilities{Installed: true, DaemonRunning: true, Authenticated: true}, TailscaleErr: errors.New("Pier is not authorized to use Funnel")}, "Funnel"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, _, err := runCLI(t, &fakeApp{doctor: tc.doctor}, "doctor")
			if err != nil {
				t.Fatalf("doctor error = %v", err)
			}
			if !strings.Contains(stdout, tc.want) {
				t.Fatalf("doctor output = %q, want %q", stdout, tc.want)
			}
		})
	}
}

func TestServiceNotFoundCommands(t *testing.T) {
	missing := &app.ServiceNotFoundError{Name: "missing"}
	commands := []struct {
		args []string
		fake *fakeApp
	}{
		{[]string{"share", "missing"}, &fakeApp{shareErr: missing}},
		{[]string{"unshare", "missing"}, &fakeApp{unshareErr: missing}},
		{[]string{"pause", "missing"}, &fakeApp{pauseErr: missing}},
		{[]string{"resume", "missing"}, &fakeApp{resumeErr: missing}},
		{[]string{"open", "missing"}, &fakeApp{openErr: missing}},
		{[]string{"copy", "missing"}, &fakeApp{copyErr: missing}},
	}
	for _, tc := range commands {
		_, stderr, err := runCLI(t, tc.fake, tc.args...)
		if !errors.As(err, new(*app.ServiceNotFoundError)) {
			t.Errorf("%s error = %v", tc.args[0], err)
		}
		if !strings.Contains(stderr, `service named "missing"`) {
			t.Errorf("%s stderr = %q", tc.args[0], stderr)
		}
	}
}

func TestServiceAddRequiresNameAndTargetWithoutTTY(t *testing.T) {
	_, stderr, err := runCLI(t, &fakeApp{}, "service", "add")
	if err == nil {
		t.Fatal("service add without name/target error = nil")
	}
	if !strings.Contains(stderr, "--target") {
		t.Fatalf("service add stderr = %q", stderr)
	}
}

func TestServiceAddWithFlagsPassesProtocolWithoutForm(t *testing.T) {
	fake := &fakeApp{status: app.StatusResult{}}
	stdout, _, err := runCLI(t, fake, "service", "add", "api", "--target", "localhost:4000", "--path", "/api", "--protocol", "https")
	if err != nil {
		t.Fatalf("service add error = %v", err)
	}
	if stdout == "" && err != nil {
		t.Fatal("service add produced no output")
	}
	if fake.addReq.Name != "api" || fake.addReq.Target != "localhost:4000" || fake.addReq.Path != "/api" || fake.addReq.Protocol != "https" {
		t.Fatalf("service add request = %#v", fake.addReq)
	}
}

func TestJSONHasNoColorSequences(t *testing.T) {
	stdout, _, err := runCLI(t, &fakeApp{status: app.StatusResult{
		Project:  project.Context{ID: "p", Root: "/tmp"},
		Services: []app.ServiceInfo{{Name: "web", Path: "/", Health: health.Result{Status: health.StatusHealthy}, URL: "https://host.ts.net:8443/"}},
	}}, "--json", "status")
	if err != nil {
		t.Fatalf("json status error = %v", err)
	}
	if strings.Contains(stdout, "\x1b") {
		t.Fatalf("JSON contained color sequences: %q", stdout)
	}
	if !strings.Contains(stdout, `"version": 1`) || !strings.Contains(stdout, `"command": "status"`) {
		t.Fatalf("JSON envelope = %s", stdout)
	}
}
