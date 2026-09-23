package tui

import (
	"fmt"
	"path/filepath"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/project"
	"github.com/Bethel-nz/pier/internal/state"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeComponents()
		if m.form != nil {
			m.form = m.form.WithWidth(max(36, min(56, m.width-12)))
		}
		return m, nil
	case projectsLoadedMsg:
		m.projects = msg.Projects
		m.projectCursor = min(m.projectCursor, len(m.projects))
		for index, item := range m.projects {
			if item.ProjectID == m.project.ID {
				m.projectCursor = index
				break
			}
		}
		return m, nil
	case projectAddedMsg:
		m.project = msg.Project
		m.start = msg.Project.Root
		m.overlay = overlayNone
		m.pathInput.Blur()
		m.focus = focusServices
		m.cursor = 0
		m.status = app.StatusResult{}
		m.doctor = app.DoctorResult{}
		m.projects = upsertProject(m.projects, msg.Entry)
		for index, item := range m.projects {
			if item.ProjectID == msg.Entry.ProjectID {
				m.projectCursor = index
				break
			}
		}
		m.busy = true
		m.resetActivity()
		m.logInfo("project ready", "name", msg.Entry.Name, "path", msg.Entry.Path)
		return m, tea.Batch(m.loadStatus(), m.loadDoctor())
	case statusLoadedMsg:
		m.status = msg.Status
		if !m.hasProject() {
			m.project = msg.Status.Project
		}
		if m.cursor >= len(m.status.Services) {
			m.cursor = max(0, len(m.status.Services)-1)
		}
		m.busy = false
		m.logInfo("service status refreshed", "services", len(m.status.Services))
		return m, nil
	case doctorLoadedMsg:
		m.doctor = msg.Doctor
		m.logInfo("Tailscale check finished", "status", m.connected())
		return m, nil
	case planLoadedMsg:
		m.plan = msg.Plan
		m.busy = false
		if m.confirm != "" {
			if needsConfirm(msg.Plan.Plan) {
				m.overlay = overlayConfirm
				return m, nil
			}
			return m.dispatch(m.confirm)
		}
		m.overlay = overlayPlan
		return m, nil
	case operationFinishedMsg:
		m.busy = false
		m.overlay = overlayNone
		m.confirm = ""
		m.logInfo("operation finished", "command", msg.Result.Command)
		return m, tea.Batch(m.loadStatus(), m.loadDoctor())
	case operationFailedMsg:
		m.busy = false
		m.err = msg.Err
		m.overlay = overlayError
		m.pathInput.Blur()
		m.logInfo("operation failed", "error", msg.Err)
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	if m.overlay == overlayAddService && m.form != nil {
		return m.updateAddServiceForm(msg)
	}
	return m, nil
}

func (m model) updateAddServiceForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	form, cmd := m.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.form = f
	}
	switch m.form.State {
	case huh.StateAborted:
		m.overlay = overlayNone
		m.form = nil
		return m, nil
	case huh.StateCompleted:
		values := AddServiceValues{}
		if m.addValues != nil {
			values = *m.addValues
		}
		m.overlay = overlayNone
		m.form = nil
		m.busy = true
		m.logInfo("adding service", "name", values.Name)
		return m, tea.Batch(cmd, m.addService(values))
	}
	return m, cmd
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.overlay == overlayAddService && m.form != nil {
		return m.updateAddServiceForm(msg)
	}
	if m.overlay == overlayAdd {
		if key.Matches(msg, m.keys.Cancel) {
			m.overlay = overlayNone
			m.pathInput.Blur()
			return m, nil
		}
		if key.Matches(msg, m.keys.Confirm) {
			m.busy = true
			m.logInfo("adding project", "path", m.pathInput.Value())
			return m, m.addProject(m.pathInput.Value())
		}
		var cmd tea.Cmd
		m.pathInput, cmd = m.pathInput.Update(msg)
		return m, cmd
	}
	if m.overlay == overlayError {
		m.overlay = overlayNone
		m.err = nil
		return m, nil
	}
	if m.overlay == overlayHelp {
		if key.Matches(msg, m.keys.Help) || key.Matches(msg, m.keys.Cancel) || key.Matches(msg, m.keys.Quit) {
			m.overlay = overlayNone
		}
		return m, nil
	}
	if m.overlay == overlayPlan {
		if key.Matches(msg, m.keys.Cancel) || key.Matches(msg, m.keys.Quit) || key.Matches(msg, m.keys.Plan) {
			m.overlay = overlayNone
		}
		return m, nil
	}
	if m.overlay == overlayConfirm {
		if key.Matches(msg, m.keys.Confirm) {
			return m.dispatch(m.confirm)
		}
		if key.Matches(msg, m.keys.Cancel) {
			m.overlay = overlayNone
			m.confirm = ""
			return m, nil
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.Quit) {
		m.quit = true
		return m, tea.Quit
	}
	if key.Matches(msg, m.keys.Help) {
		m.overlay = overlayHelp
		return m, nil
	}
	if key.Matches(msg, m.keys.Add) {
		if m.focus == focusServices && m.hasProject() {
			return m, m.startAddService()
		}
		return m, m.startAdd()
	}
	if key.Matches(msg, m.keys.NextPane) {
		m.focus = (m.focus + 1) % 3
		return m, nil
	}
	if key.Matches(msg, m.keys.LeftPane) {
		if m.focus > focusProjects {
			m.focus--
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.RightPane) {
		if m.focus < focusActivity {
			m.focus++
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.Confirm) && m.focus == focusProjects {
		if m.projectCursor == len(m.projects) {
			return m, m.startAdd()
		}
		return m.activateSelectedProject()
	}
	if key.Matches(msg, m.keys.Confirm) && m.focus == focusServices && !m.busy {
		service, ok := m.selected()
		if !ok {
			return m, nil
		}
		m.busy = true
		m.logInfo("opening service", "service", service.Name)
		return m, m.runOpen(service.Name)
	}
	if key.Matches(msg, m.keys.SelectDown) {
		switch m.focus {
		case focusProjects:
			if m.projectCursor < len(m.projects) {
				m.projectCursor++
			}
		case focusServices:
			if m.cursor+1 < len(m.status.Services) {
				m.cursor++
			}
		case focusActivity:
			m.activity.LineDown(1)
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.SelectUp) {
		switch m.focus {
		case focusProjects:
			if m.projectCursor > 0 {
				m.projectCursor--
			}
		case focusServices:
			if m.cursor > 0 {
				m.cursor--
			}
		case focusActivity:
			m.activity.LineUp(1)
		}
		return m, nil
	}
	if m.busy {
		return m, nil
	}
	if key.Matches(msg, m.keys.Refresh) {
		if !m.hasProject() {
			return m, m.loadProjects()
		}
		m.busy = true
		m.pending = append(m.pending, "refresh")
		m.logInfo("refreshing project")
		return m, tea.Batch(m.loadStatus(), m.loadDoctor())
	}
	if key.Matches(msg, m.keys.Plan) {
		if !m.hasProject() {
			return m, nil
		}
		m.busy = true
		m.confirm = ""
		m.pending = append(m.pending, "plan")
		return m, m.loadPlan()
	}
	if key.Matches(msg, m.keys.Up) {
		if !m.hasProject() {
			return m, nil
		}
		m.pending = append(m.pending, "up")
		m.confirm = "up"
		m.busy = true
		return m, m.loadPlan()
	}
	if key.Matches(msg, m.keys.Down) {
		if !m.hasProject() {
			return m, nil
		}
		m.pending = append(m.pending, "down")
		m.confirm = "down"
		m.busy = true
		return m, m.loadPlan()
	}
	if key.Matches(msg, m.keys.Share) {
		service, ok := m.selected()
		if !ok {
			return m, nil
		}
		action := "share"
		if service.Public {
			action = "unshare"
		}
		m.pending = append(m.pending, action)
		m.confirm = action
		m.busy = true
		return m, m.loadPlan()
	}
	if key.Matches(msg, m.keys.Pause) {
		if m.focus != focusServices {
			return m, nil
		}
		service, ok := m.selected()
		if !ok {
			return m, nil
		}
		if service.Paused {
			m.busy = true
			m.pending = append(m.pending, "resume")
			m.logInfo("resuming service route", "service", service.Name)
			return m, m.runResume(service.Name)
		}
		m.pending = append(m.pending, "pause")
		m.confirm = "pause"
		m.overlay = overlayConfirm
		return m, nil
	}
	if key.Matches(msg, m.keys.Copy) {
		service, ok := m.selected()
		if !ok {
			return m, nil
		}
		m.busy = true
		m.pending = append(m.pending, "copy")
		return m, m.runCopy(service.Name)
	}
	if key.Matches(msg, m.keys.Open) {
		service, ok := m.selected()
		if !ok {
			return m, nil
		}
		m.busy = true
		m.pending = append(m.pending, "open")
		return m, m.runOpen(service.Name)
	}
	return m, nil
}

func (m model) activateSelectedProject() (tea.Model, tea.Cmd) {
	if m.projectCursor < 0 || m.projectCursor >= len(m.projects) {
		return m, nil
	}
	entry := m.projects[m.projectCursor]
	proj, err := project.Find(filepath.Join(entry.Path, "pier.yaml"))
	if err != nil {
		m.err = fmt.Errorf("open %s: %w", entry.Name, err)
		m.overlay = overlayError
		m.logInfo("project unavailable", "name", entry.Name, "path", entry.Path)
		return m, nil
	}
	m.project = proj
	m.start = proj.Root
	m.status = app.StatusResult{}
	m.doctor = app.DoctorResult{}
	m.cursor = 0
	m.focus = focusServices
	m.busy = true
	m.resetActivity()
	m.logInfo("opening project", "name", entry.Name)
	return m, tea.Batch(m.loadStatus(), m.loadDoctor())
}

func (m *model) resizeComponents() {
	_, _, activityWidth := m.widePanelWidths()
	_, activityHeight := m.widePanelHeights()
	contentWidth := max(20, activityWidth-4)
	contentHeight := max(3, activityHeight-4)
	if m.compact() {
		contentWidth = max(20, m.width-4)
		contentHeight = max(3, m.height-6)
	}
	m.activity.Width = contentWidth
	m.activity.Height = contentHeight
	m.syncActivity()
	m.pathInput.Width = max(20, min(60, m.width-16))
}

func upsertProject(projects []state.ProjectState, entry state.ProjectState) []state.ProjectState {
	result := append([]state.ProjectState(nil), projects...)
	for index := range result {
		if result[index].ProjectID == entry.ProjectID {
			result[index] = entry
			return result
		}
	}
	return append(result, entry)
}

func (m model) dispatch(action string) (model, tea.Cmd) {
	m.overlay = overlayNone
	m.busy = true
	switch action {
	case "up":
		return m, m.runUp()
	case "down":
		return m, m.runDown()
	case "share", "unshare":
		service, ok := m.selected()
		if !ok {
			m.busy = false
			m.confirm = ""
			return m, nil
		}
		return m, m.runShare(service.Name, service.Public)
	case "pause":
		service, ok := m.selected()
		if !ok {
			m.busy = false
			m.confirm = ""
			return m, nil
		}
		m.logInfo("pausing service route", "service", service.Name)
		return m, m.runPause(service.Name)
	}
	m.busy = false
	m.confirm = ""
	return m, nil
}
