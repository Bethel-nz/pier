package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/health"
	"github.com/Bethel-nz/pier/internal/project"
	"github.com/Bethel-nz/pier/internal/reconcile"
	"github.com/Bethel-nz/pier/internal/state"
)

type fakeTUI struct {
	calls   []string
	status  app.StatusResult
	doctor  app.DoctorResult
	plan    app.PlanResult
	planErr error
}

type fakeCatalog struct {
	projects []state.ProjectState
}

func (f *fakeCatalog) List() ([]state.ProjectState, error) {
	return append([]state.ProjectState(nil), f.projects...), nil
}

func (f *fakeCatalog) Load(id string) (state.ProjectState, error) {
	for _, item := range f.projects {
		if item.ProjectID == id {
			return item, nil
		}
	}
	return state.ProjectState{Version: state.CurrentVersion}, nil
}

func (f *fakeCatalog) Save(item state.ProjectState) error {
	for index := range f.projects {
		if f.projects[index].ProjectID == item.ProjectID {
			f.projects[index] = item
			return nil
		}
	}
	f.projects = append(f.projects, item)
	return nil
}

func (f *fakeTUI) Status(context.Context, app.StatusRequest) (app.StatusResult, error) {
	f.calls = append(f.calls, "status")
	return f.status, nil
}
func (f *fakeTUI) Doctor(context.Context, app.DoctorRequest) (app.DoctorResult, error) {
	f.calls = append(f.calls, "doctor")
	return f.doctor, nil
}
func (f *fakeTUI) Plan(context.Context, app.PlanRequest) (app.PlanResult, error) {
	f.calls = append(f.calls, "plan")
	return f.plan, f.planErr
}
func (f *fakeTUI) Up(context.Context, app.UpRequest) (app.UpResult, error) {
	f.calls = append(f.calls, "up")
	return app.UpResult{Plan: f.plan.Plan, Services: f.status.Services}, nil
}
func (f *fakeTUI) Down(context.Context, app.DownRequest) (app.DownResult, error) {
	f.calls = append(f.calls, "down")
	return app.DownResult{Plan: f.plan.Plan}, nil
}
func (f *fakeTUI) Share(_ context.Context, req app.ShareRequest) (app.ShareResult, error) {
	f.calls = append(f.calls, "share:"+req.Service)
	return app.ShareResult{}, nil
}
func (f *fakeTUI) Unshare(_ context.Context, req app.UnshareRequest) (app.UnshareResult, error) {
	f.calls = append(f.calls, "unshare:"+req.Service)
	return app.UnshareResult{}, nil
}
func (f *fakeTUI) Pause(_ context.Context, req app.PauseRequest) (app.PauseResult, error) {
	f.calls = append(f.calls, "pause:"+req.Service)
	return app.PauseResult{}, nil
}
func (f *fakeTUI) Resume(_ context.Context, req app.ResumeRequest) (app.ResumeResult, error) {
	f.calls = append(f.calls, "resume:"+req.Service)
	return app.ResumeResult{}, nil
}
func (f *fakeTUI) AddService(_ context.Context, req app.AddServiceRequest) (app.AddServiceResult, error) {
	f.calls = append(f.calls, "add:"+req.Name)
	return app.AddServiceResult{Service: app.ServiceInfo{Name: req.Name, Target: req.Target, Path: req.Path, Public: req.Public}}, nil
}
func (f *fakeTUI) Open(_ context.Context, req app.OpenRequest) (app.OpenResult, error) {
	f.calls = append(f.calls, "open:"+req.Service)
	return app.OpenResult{URL: "https://host.ts.net:8443/"}, nil
}
func (f *fakeTUI) Copy(_ context.Context, req app.CopyRequest) (app.CopyResult, error) {
	f.calls = append(f.calls, "copy:"+req.Service)
	return app.CopyResult{URL: "https://host.ts.net:8443/"}, nil
}

func testModel(fake *fakeTUI) model {
	fake.status = app.StatusResult{Services: []app.ServiceInfo{
		{Name: "web", Host: "localhost", Port: 3000, Path: "/", Health: health.Result{Status: health.StatusHealthy}, URL: "https://host.ts.net:8443/"},
		{Name: "api", Host: "localhost", Port: 4000, Path: "/api", Health: health.Result{Status: health.StatusHealthy}, URL: "https://host.ts.net:8443/api"},
		{Name: "webhook", Host: "localhost", Port: 8787, Path: "/hooks", Public: true, Health: health.Result{Status: health.StatusHealthy}, URL: "https://host.ts.net/hooks"},
	}}
	fake.doctor = app.DoctorResult{}
	m := newModel(context.Background(), fake, project.Context{Root: "/tmp/greppa"}, Options{Width: 100, Height: 24})
	updated, _ := m.Update(statusLoadedMsg{Status: fake.status})
	return updated.(model)
}

func keyRunes(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestInitialLoadRequestsStatusAndDoctor(t *testing.T) {
	fake := &fakeTUI{}
	m := newModel(context.Background(), fake, project.Context{Root: "/tmp/greppa"}, Options{Width: 100})
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned nil")
	}
	runCmd(t, cmd, fake)
	if !contains(fake.calls, "status") || !contains(fake.calls, "doctor") {
		t.Fatalf("Init() calls = %q, want status and doctor", fake.calls)
	}
}

func TestNoProjectStartsAtProjectLauncherWithoutCallingBackend(t *testing.T) {
	fake := &fakeTUI{}
	catalog := &fakeCatalog{}
	dir := t.TempDir()
	m := newModelWithCatalog(context.Background(), fake, project.Context{}, Options{Start: dir, Width: 100}, catalog)

	msgs := runCmd(t, m.Init(), fake)
	if len(msgs) != 1 {
		t.Fatalf("Init() messages = %d, want project list", len(msgs))
	}
	updated, _ := m.Update(msgs[0])
	m = updated.(model)
	if len(fake.calls) != 0 {
		t.Fatalf("backend calls = %q, want none without a project", fake.calls)
	}
	if !strings.Contains(m.View(), "Initialize here") {
		t.Fatalf("launcher missing initialize action: %q", m.View())
	}
}

func TestAddProjectInitializesDirectoryAndRegistersIt(t *testing.T) {
	fake := &fakeTUI{}
	catalog := &fakeCatalog{}
	dir := t.TempDir()
	m := newModelWithCatalog(context.Background(), fake, project.Context{}, Options{Start: dir, Width: 100}, catalog)
	m.startAdd()
	m.pathInput.SetValue(dir)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	msgs := runCmd(t, cmd, fake)
	if len(msgs) != 1 {
		t.Fatalf("add messages = %d, want one", len(msgs))
	}
	updated, _ = m.Update(msgs[0])
	m = updated.(model)

	if _, err := os.Stat(filepath.Join(dir, "pier.yaml")); err != nil {
		t.Fatalf("pier.yaml was not created: %v", err)
	}
	if len(catalog.projects) != 1 || catalog.projects[0].Path != dir {
		t.Fatalf("registered projects = %#v, want %s", catalog.projects, dir)
	}
	if m.project.Root != dir {
		t.Fatalf("selected project root = %q, want %q", m.project.Root, dir)
	}
}

func TestArrowKeysStayInBounds(t *testing.T) {
	m := testModel(&fakeTUI{})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(model)
	if m.cursor != 0 {
		t.Fatalf("up at start cursor = %d", m.cursor)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if m.cursor != 1 {
		t.Fatalf("down cursor = %d", m.cursor)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if m.cursor != 2 {
		t.Fatalf("down past end cursor = %d", m.cursor)
	}
}

func TestKeysDispatchAsyncCommands(t *testing.T) {
	fake := &fakeTUI{}
	m := testModel(fake)
	cases := []struct {
		key  tea.KeyMsg
		want string
	}{
		{keyRunes('u'), "plan"},
		{keyRunes('d'), "plan"},
		{keyRunes('p'), "plan"},
		{keyRunes('r'), "status"},
	}
	for _, tc := range cases {
		fake.calls = nil
		m.busy = false
		m.overlay = overlayNone
		updated, cmd := m.Update(tc.key)
		m = updated.(model)
		if cmd == nil {
			t.Fatalf("key %v dispatched nil cmd", tc.key)
		}
		runCmd(t, cmd, fake)
		if !contains(fake.calls, tc.want) {
			t.Fatalf("key %v calls = %q, want %s", tc.key, fake.calls, tc.want)
		}
	}
}

func TestShareUsesEffectivePublic(t *testing.T) {
	fake := &fakeTUI{}
	m := testModel(fake)
	m.cursor = 2
	m.busy = false
	updated, cmd := m.Update(keyRunes('s'))
	m = updated.(model)
	runCmd(t, cmd, fake)
	if m.pending[len(m.pending)-1] != "unshare" {
		t.Fatalf("share on public service pending = %q", m.pending)
	}
	m.cursor = 0
	m.busy = false
	m.confirm = ""
	updated, cmd = m.Update(keyRunes('s'))
	m = updated.(model)
	if m.pending[len(m.pending)-1] != "share" {
		t.Fatalf("share on tailnet service pending = %q", m.pending)
	}
}

func TestCopyAndOpenUseSelectedService(t *testing.T) {
	fake := &fakeTUI{}
	m := testModel(fake)
	m.cursor = 1
	updated, cmd := m.Update(keyRunes('c'))
	m = updated.(model)
	runCmd(t, cmd, fake)
	if !contains(fake.calls, "copy:api") {
		t.Fatalf("copy calls = %q", fake.calls)
	}
	m.busy = false
	fake.calls = nil
	updated, cmd = m.Update(keyRunes('o'))
	runCmd(t, cmd, fake)
	if !contains(fake.calls, "open:api") {
		t.Fatalf("open calls = %q", fake.calls)
	}
}

func TestEnterOpensSelectedServiceFromServicePane(t *testing.T) {
	fake := &fakeTUI{}
	m := testModel(fake)
	m.cursor = 1

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	runCmd(t, cmd, fake)
	if !contains(fake.calls, "open:api") {
		t.Fatalf("enter calls = %q, want selected service opened", fake.calls)
	}
}

func TestMutationsRequireConfirmationForDeletes(t *testing.T) {
	fake := &fakeTUI{plan: app.PlanResult{Plan: reconcile.Plan{Operations: []reconcile.Operation{
		{Kind: reconcile.KindDelete, Before: reconcile.Route{HTTPSPort: 8443, Path: "/"}},
	}}}}
	m := testModel(fake)
	updated, cmd := m.Update(keyRunes('d'))
	m = updated.(model)
	msg := runCmd(t, cmd, fake)[0]
	updated, _ = m.Update(msg)
	m = updated.(model)
	if m.overlay != overlayConfirm {
		t.Fatalf("overlay = %d, want confirm", m.overlay)
	}
	updated, _ = m.Update(keyRunes('q'))
	m = updated.(model)
	if m.quit {
		t.Fatal("q quit while confirmation was active")
	}
}

func TestBusyDisablesConflictingActions(t *testing.T) {
	m := testModel(&fakeTUI{})
	m.busy = true
	updated, cmd := m.Update(keyRunes('u'))
	if cmd != nil {
		t.Fatal("busy model dispatched a command")
	}
	if updated.(model).pending != nil && contains(updated.(model).pending, "up") {
		t.Fatal("busy model queued up")
	}
}

func TestErrorsStayUntilDismissed(t *testing.T) {
	m := testModel(&fakeTUI{})
	updated, _ := m.Update(operationFailedMsg{Err: errors.New("Pier boom")})
	m = updated.(model)
	if m.overlay != overlayError || !strings.Contains(m.View(), "Pier boom") {
		t.Fatalf("error overlay missing: overlay=%d view=%q", m.overlay, m.View())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if m.overlay == overlayError {
		t.Fatal("error overlay was not dismissed")
	}
}

func TestResizeSwitchesToCompactView(t *testing.T) {
	m := testModel(&fakeTUI{})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 70, Height: 24})
	m = updated.(model)
	view := m.View()
	if !m.compact() {
		t.Fatal("expected compact layout below 80 columns")
	}
	if !strings.Contains(view, ">") {
		t.Fatalf("missing selected-row marker in %q", view)
	}
}

func TestWideViewRendersActivityAsItsOwnPanel(t *testing.T) {
	m := testModel(&fakeTUI{})
	m.width = 120
	m.height = 32
	m.resizeComponents()

	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) < 2 || strings.Count(lines[1], "╭") != 2 {
		t.Fatalf("wide view should start with a sidebar and project panel\n%s", view)
	}
	leftWidth, _, _ := m.widePanelWidths()
	activityTop := "│" + strings.Repeat(" ", leftWidth-2) + "│╭"
	if !strings.Contains(view, "\n"+activityTop) {
		t.Fatalf("activity panel should sit below the project view on the right\n%s", view)
	}
	if !strings.Contains(view, iconActivity+" ACTIVITY · greppa") {
		t.Fatalf("activity panel is not tied to the selected project\n%s", view)
	}
}

func TestSelectingProjectStartsFreshProjectActivity(t *testing.T) {
	m := testModel(&fakeTUI{})
	m.logInfo("event from previous project")
	entry := state.ProjectState{ProjectID: "next", Name: "next", Path: "/tmp/next"}

	updated, _ := m.Update(projectAddedMsg{
		Project: project.Context{ID: "next", Root: "/tmp/next", ConfigPath: "/tmp/next/pier.yaml"},
		Entry:   entry,
	})
	m = updated.(model)
	activity := m.activityLog.String()
	if strings.Contains(activity, "previous project") {
		t.Fatalf("new project activity retained an event from the previous project: %q", activity)
	}
	if !strings.Contains(activity, "project ready") {
		t.Fatalf("new project activity = %q, want project ready event", activity)
	}
}

func TestActivityWrapsLongEntriesWithinItsPanel(t *testing.T) {
	m := testModel(&fakeTUI{})
	m.width = 160
	m.height = 32
	m.resizeComponents()
	m.resetActivity()
	m.logInfo("service status refreshed", "services", 123)

	if !strings.Contains(m.activity.View(), "services=123") {
		t.Fatalf("activity viewport truncated the end of a wrapped entry: %q", m.activity.View())
	}
}

func TestServiceRowsUseReadableStatusAndAccessIcons(t *testing.T) {
	m := testModel(&fakeTUI{})
	m.width = 120
	m.doctor.Capabilities.Authenticated = true

	view := m.View()
	for _, want := range []string{
		iconConnected + " connected",
		iconHealthy + " healthy",
		iconTailnet + " tailnet",
		iconPublic + " public",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q\n%s", want, view)
		}
	}
}

func TestAddOnServicesOpensServiceForm(t *testing.T) {
	m := testModel(&fakeTUI{})
	updated, cmd := m.Update(keyRunes('a'))
	m = updated.(model)
	if m.overlay != overlayAddService || m.form == nil {
		t.Fatalf("overlay = %d form nil=%t, want add-service form", m.overlay, m.form == nil)
	}
	if cmd == nil {
		t.Fatal("add-service form Init() was not returned")
	}
}

func TestSpacePausesAndResumesSelectedService(t *testing.T) {
	fake := &fakeTUI{}
	m := testModel(fake)
	m.cursor = 1
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	m = updated.(model)
	if m.overlay != overlayConfirm || m.confirm != "pause" {
		t.Fatalf("pause overlay = %d confirm = %q", m.overlay, m.confirm)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	runCmd(t, cmd, fake)
	if !contains(fake.calls, "pause:api") {
		t.Fatalf("pause calls = %q", fake.calls)
	}

	m.status.Services[1].Paused = true
	m.busy = false
	m.overlay = overlayNone
	fake.calls = nil
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	runCmd(t, cmd, fake)
	if !contains(fake.calls, "resume:api") {
		t.Fatalf("resume calls = %q", fake.calls)
	}
}

func TestQuitWhenIdle(t *testing.T) {
	m := testModel(&fakeTUI{})
	updated, cmd := m.Update(keyRunes('q'))
	m = updated.(model)
	if !m.quit || cmd == nil {
		t.Fatal("q should quit when idle")
	}
}

func runCmd(t *testing.T, cmd tea.Cmd, fake *fakeTUI) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var msgs []tea.Msg
		for _, next := range batch {
			if next != nil {
				msgs = append(msgs, next())
			}
		}
		return msgs
	}
	return []tea.Msg{msg}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
