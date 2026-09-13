package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pier/internal/app"
	"pier/internal/project"
	"pier/internal/reconcile"
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
)

type statusLoadedMsg struct{ Status app.StatusResult }
type doctorLoadedMsg struct{ Doctor app.DoctorResult }
type planLoadedMsg struct{ Plan app.PlanResult }
type operationFinishedMsg struct{ Result app.OperationResult }
type operationFailedMsg struct{ Err error }
type tickMsg time.Time

type backend interface {
	Status(ctx context.Context, req app.StatusRequest) (app.StatusResult, error)
	Doctor(ctx context.Context, req app.DoctorRequest) (app.DoctorResult, error)
	Plan(ctx context.Context, req app.PlanRequest) (app.PlanResult, error)
	Up(ctx context.Context, req app.UpRequest) (app.UpResult, error)
	Down(ctx context.Context, req app.DownRequest) (app.DownResult, error)
	Share(ctx context.Context, req app.ShareRequest) (app.ShareResult, error)
	Unshare(ctx context.Context, req app.UnshareRequest) (app.UnshareResult, error)
	Open(ctx context.Context, req app.OpenRequest) (app.OpenResult, error)
	Copy(ctx context.Context, req app.CopyRequest) (app.CopyResult, error)
}

type model struct {
	ctx     context.Context
	app     backend
	project project.Context
	start   string
	keys    keyMap

	width  int
	height int

	status app.StatusResult
	doctor app.DoctorResult
	plan   app.PlanResult
	cursor int

	overlay overlay
	busy    bool
	pending []string
	err     error
	confirm string
	quit    bool
}

func newModel(ctx context.Context, svc backend, proj project.Context, options Options) model {
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
	return model{
		ctx:     ctx,
		app:     svc,
		project: proj,
		start:   start,
		keys:    newKeyMap(),
		width:   width,
		height:  height,
	}
}

func (m model) Init() tea.Cmd {
	m.pending = []string{"status", "doctor"}
	return tea.Batch(m.loadStatus(), m.loadDoctor())
}

func (m model) selected() (app.ServiceInfo, bool) {
	if m.cursor < 0 || m.cursor >= len(m.status.Services) {
		return app.ServiceInfo{}, false
	}
	return m.status.Services[m.cursor], true
}

func (m model) compact() bool {
	return m.width < 80
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
