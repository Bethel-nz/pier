package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	charmlog "github.com/charmbracelet/log"
	"github.com/charmbracelet/x/ansi"

	"pier/internal/app"
	"pier/internal/config"
	"pier/internal/project"
	"pier/internal/reconcile"
	"pier/internal/state"
)

// Options configure the TUI session.
type Options struct {
	Width   int
	Height  int
	NoColor bool
	Start   string
}

type overlay int

const (
	overlayNone overlay = iota
	overlayHelp
	overlayPlan
	overlayConfirm
	overlayError
	overlayAdd
	overlayAddService
)

type focusPane int

const (
	focusProjects focusPane = iota
	focusServices
	focusActivity
)

type statusLoadedMsg struct{ Status app.StatusResult }
type doctorLoadedMsg struct{ Doctor app.DoctorResult }
type planLoadedMsg struct{ Plan app.PlanResult }
type operationFinishedMsg struct{ Result app.OperationResult }
type operationFailedMsg struct{ Err error }
type tickMsg time.Time
type projectsLoadedMsg struct{ Projects []state.ProjectState }
type projectAddedMsg struct {
	Project project.Context
	Entry   state.ProjectState
}

type projectCatalog interface {
	List() ([]state.ProjectState, error)
	Load(string) (state.ProjectState, error)
	Save(state.ProjectState) error
}

type backend interface {
	Status(ctx context.Context, req app.StatusRequest) (app.StatusResult, error)
	Doctor(ctx context.Context, req app.DoctorRequest) (app.DoctorResult, error)
	Plan(ctx context.Context, req app.PlanRequest) (app.PlanResult, error)
	Up(ctx context.Context, req app.UpRequest) (app.UpResult, error)
	Down(ctx context.Context, req app.DownRequest) (app.DownResult, error)
	Share(ctx context.Context, req app.ShareRequest) (app.ShareResult, error)
	Unshare(ctx context.Context, req app.UnshareRequest) (app.UnshareResult, error)
	Pause(ctx context.Context, req app.PauseRequest) (app.PauseResult, error)
	Resume(ctx context.Context, req app.ResumeRequest) (app.ResumeResult, error)
	AddService(ctx context.Context, req app.AddServiceRequest) (app.AddServiceResult, error)
	Open(ctx context.Context, req app.OpenRequest) (app.OpenResult, error)
	Copy(ctx context.Context, req app.CopyRequest) (app.CopyResult, error)
}

type model struct {
	ctx     context.Context
	app     backend
	project project.Context
	start   string
	keys    keyMap

	width        int
	height       int
	disableColor bool

	status app.StatusResult
	doctor app.DoctorResult
	plan   app.PlanResult
	cursor int

	catalog       projectCatalog
	projects      []state.ProjectState
	projectCursor int
	focus         focusPane
	pathInput     textinput.Model
	activity      viewport.Model
	activityLog   *activityWriter
	logger        *charmlog.Logger

	overlay   overlay
	busy      bool
	pending   []string
	err       error
	confirm   string
	quit      bool
	form      *huh.Form
	addValues *AddServiceValues
}

func newModel(ctx context.Context, svc backend, proj project.Context, options Options) model {
	return newModelWithCatalog(ctx, svc, proj, options, nil)
}

func newModelWithCatalog(ctx context.Context, svc backend, proj project.Context, options Options, catalog projectCatalog) model {
	width := options.Width
	if width == 0 {
		width = 100
	}
	height := options.Height
	if height == 0 {
		height = 24
	}
	start := options.Start
	if start == "" {
		start = proj.Root
	}
	if start == "" {
		start = "."
	}
	input := textinput.New()
	input.Prompt = "Path  "
	input.Placeholder = "/path/to/project"
	input.CharLimit = 1024
	input.Width = 54
	activity := viewport.New(70, 6)
	activity.MouseWheelEnabled = true
	activityLog, logger := newActivityLog()
	focus := focusServices
	if proj.ID == "" && proj.Root == "" && catalog != nil {
		focus = focusProjects
	}
	return model{
		ctx:          ctx,
		app:          svc,
		project:      proj,
		start:        start,
		keys:         newKeyMap(),
		width:        width,
		height:       height,
		disableColor: options.NoColor,
		catalog:      catalog,
		focus:        focus,
		pathInput:    input,
		activity:     activity,
		activityLog:  activityLog,
		logger:       logger,
	}
}

func (m model) Init() tea.Cmd {
	commands := []tea.Cmd{}
	if m.catalog != nil {
		commands = append(commands, m.loadProjects())
	}
	if m.project.ID != "" || m.catalog == nil {
		m.pending = []string{"status", "doctor"}
		commands = append(commands, m.loadStatus(), m.loadDoctor())
	}
	return tea.Batch(commands...)
}

func (m model) selected() (app.ServiceInfo, bool) {
	if m.cursor < 0 || m.cursor >= len(m.status.Services) {
		return app.ServiceInfo{}, false
	}
	return m.status.Services[m.cursor], true
}

func (m model) compact() bool {
	return m.width < 104
}

func (m model) hasProject() bool {
	return m.project.ID != "" || m.project.Root != ""
}

func (m model) loadProjects() tea.Cmd {
	return func() tea.Msg {
		if m.project.ID != "" {
			entry, err := m.catalog.Load(m.project.ID)
			if err != nil {
				return operationFailedMsg{Err: err}
			}
			cfg, err := config.Load(m.project.ConfigPath)
			if err != nil {
				return operationFailedMsg{Err: err}
			}
			entry.Version = state.CurrentVersion
			entry.ProjectID = m.project.ID
			entry.Name = cfg.Name
			if entry.Name == "" {
				entry.Name = filepath.Base(m.project.Root)
			}
			entry.Path = m.project.Root
			entry.UpdatedAt = time.Now()
			if err := m.catalog.Save(entry); err != nil {
				return operationFailedMsg{Err: err}
			}
		}
		projects, err := m.catalog.List()
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		return projectsLoadedMsg{Projects: projects}
	}
}

func (m *model) startAddService() tea.Cmd {
	m.addValues = &AddServiceValues{Target: "localhost:3000", Path: "/", Protocol: string(config.ProtocolHTTP)}
	m.form = newAddServiceForm(m.addValues, existingServiceNames(m.status.Services)).WithWidth(max(36, min(56, m.width-12)))
	m.overlay = overlayAddService
	return m.form.Init()
}

func existingServiceNames(services []app.ServiceInfo) []string {
	names := make([]string, 0, len(services))
	for _, service := range services {
		names = append(names, service.Name)
	}
	return names
}

func (m model) addService(values AddServiceValues) tea.Cmd {
	return func() tea.Msg {
		if !m.hasProject() {
			return operationFailedMsg{Err: errors.New("select a project before adding a service")}
		}
		result, err := m.app.AddService(m.ctx, app.AddServiceRequest{
			Start:    m.start,
			Name:     strings.TrimSpace(values.Name),
			Target:   strings.TrimSpace(values.Target),
			Path:     strings.TrimSpace(values.Path),
			Protocol: values.Protocol,
			Public:   values.Public,
		})
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		return operationFinishedMsg{Result: app.OperationResult{Command: "add", Services: []app.ServiceInfo{result.Service}}}
	}
}

func (m *model) startAdd() tea.Cmd {
	m.overlay = overlayAdd
	path := m.start
	if path == "" || path == "." {
		path, _ = os.Getwd()
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		path = filepath.Dir(path)
	}
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	m.pathInput.SetValue(path)
	m.pathInput.CursorEnd()
	return m.pathInput.Focus()
}

func (m model) addProject(path string) tea.Cmd {
	return func() tea.Msg {
		if m.catalog == nil {
			return operationFailedMsg{Err: errors.New("project registry is unavailable")}
		}
		path = strings.TrimSpace(path)
		if path == "" {
			return operationFailedMsg{Err: errors.New("project path is required")}
		}
		absolute, err := filepath.Abs(filepath.Clean(path))
		if err != nil {
			return operationFailedMsg{Err: fmt.Errorf("resolve project path: %w", err)}
		}
		if filepath.Base(absolute) == "pier.yaml" {
			absolute = filepath.Dir(absolute)
		}
		info, err := os.Stat(absolute)
		if err != nil {
			return operationFailedMsg{Err: fmt.Errorf("inspect project directory: %w", err)}
		}
		if !info.IsDir() {
			return operationFailedMsg{Err: fmt.Errorf("project path is not a directory: %s", absolute)}
		}

		configPath := filepath.Join(absolute, "pier.yaml")
		var proj project.Context
		if _, err := os.Stat(configPath); err == nil {
			proj, err = project.Find(configPath)
		} else if errors.Is(err, os.ErrNotExist) {
			proj, err = project.Init(absolute, filepath.Base(absolute))
		} else {
			return operationFailedMsg{Err: fmt.Errorf("inspect project configuration: %w", err)}
		}
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		cfg, err := config.Load(proj.ConfigPath)
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		entry, err := m.catalog.Load(proj.ID)
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		entry.Version = state.CurrentVersion
		entry.ProjectID = proj.ID
		entry.Name = cfg.Name
		if entry.Name == "" {
			entry.Name = filepath.Base(proj.Root)
		}
		entry.Path = proj.Root
		entry.UpdatedAt = time.Now()
		if err := m.catalog.Save(entry); err != nil {
			return operationFailedMsg{Err: err}
		}
		return projectAddedMsg{Project: proj, Entry: entry}
	}
}

func (m *model) logInfo(message string, keyvals ...interface{}) {
	if m.logger == nil {
		return
	}
	m.logger.Info(message, keyvals...)
	m.syncActivity()
	m.activity.GotoBottom()
}

func (m *model) syncActivity() {
	content := m.activityLog.String()
	if m.activity.Width > 0 {
		content = ansi.Wordwrap(content, m.activity.Width, "")
	}
	m.activity.SetContent(content)
}

func (m *model) resetActivity() {
	if m.activityLog != nil {
		m.activityLog.Reset()
	}
	m.activity.SetContent("")
	m.activity.GotoTop()
}

func (m model) connected() string {
	if m.doctor.Capabilities.Authenticated && m.doctor.TailscaleErr == nil {
		return "connected"
	}
	if m.doctor.TailscaleErr != nil {
		return "unavailable"
	}
	return "unknown"
}

func (m model) loadStatus() tea.Cmd {
	return func() tea.Msg {
		result, err := m.app.Status(m.ctx, app.StatusRequest{Start: m.start})
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		return statusLoadedMsg{Status: result}
	}
}

func (m model) loadDoctor() tea.Cmd {
	return func() tea.Msg {
		result, err := m.app.Doctor(m.ctx, app.DoctorRequest{Start: m.start})
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		return doctorLoadedMsg{Doctor: result}
	}
}

func (m model) loadPlan() tea.Cmd {
	return func() tea.Msg {
		result, err := m.app.Plan(m.ctx, app.PlanRequest{Start: m.start})
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		return planLoadedMsg{Plan: result}
	}
}

func (m model) runUp() tea.Cmd {
	return func() tea.Msg {
		result, err := m.app.Up(m.ctx, app.UpRequest{Start: m.start})
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		return operationFinishedMsg{Result: app.OperationResult{Command: "up", Plan: result.Plan, Services: result.Services}}
	}
}

func (m model) runDown() tea.Cmd {
	return func() tea.Msg {
		result, err := m.app.Down(m.ctx, app.DownRequest{Start: m.start})
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		return operationFinishedMsg{Result: app.OperationResult{Command: "down", Plan: result.Plan}}
	}
}

func (m model) runShare(name string, public bool) tea.Cmd {
	return func() tea.Msg {
		if public {
			result, err := m.app.Unshare(m.ctx, app.UnshareRequest{Start: m.start, Service: name})
			if err != nil {
				return operationFailedMsg{Err: err}
			}
			return operationFinishedMsg{Result: app.OperationResult{Command: "unshare", Plan: result.Plan, Services: []app.ServiceInfo{result.Service}}}
		}
		result, err := m.app.Share(m.ctx, app.ShareRequest{Start: m.start, Service: name})
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		return operationFinishedMsg{Result: app.OperationResult{Command: "share", Plan: result.Plan, Services: []app.ServiceInfo{result.Service}}}
	}
}

func (m model) runOpen(name string) tea.Cmd {
	return func() tea.Msg {
		result, err := m.app.Open(m.ctx, app.OpenRequest{Start: m.start, Service: name})
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		return operationFinishedMsg{Result: app.OperationResult{Command: "open", Services: []app.ServiceInfo{{Name: name, URL: result.URL}}}}
	}
}

func (m model) runCopy(name string) tea.Cmd {
	return func() tea.Msg {
		result, err := m.app.Copy(m.ctx, app.CopyRequest{Start: m.start, Service: name})
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		return operationFinishedMsg{Result: app.OperationResult{Command: "copy", Services: []app.ServiceInfo{{Name: name, URL: result.URL}}}}
	}
}

func (m model) runPause(name string) tea.Cmd {
	return func() tea.Msg {
		result, err := m.app.Pause(m.ctx, app.PauseRequest{Start: m.start, Service: name})
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		return operationFinishedMsg{Result: app.OperationResult{Command: "pause", Plan: result.Plan, Services: []app.ServiceInfo{result.Service}}}
	}
}

func (m model) runResume(name string) tea.Cmd {
	return func() tea.Msg {
		result, err := m.app.Resume(m.ctx, app.ResumeRequest{Start: m.start, Service: name})
		if err != nil {
			return operationFailedMsg{Err: err}
		}
		return operationFinishedMsg{Result: app.OperationResult{Command: "resume", Plan: result.Plan, Services: []app.ServiceInfo{result.Service}}}
	}
}

func needsConfirm(plan reconcile.Plan) bool {
	if len(plan.Conflicts) > 0 {
		return true
	}
	for _, op := range plan.Operations {
		if op.Kind == reconcile.KindDelete {
			return true
		}
	}
	return false
}
