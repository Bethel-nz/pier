package tui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case statusLoadedMsg:
		m.status = msg.Status
		if m.project.ID == "" {
			m.project = msg.Status.Project
		}
		if m.cursor >= len(m.status.Services) {
			m.cursor = max(0, len(m.status.Services)-1)
		}
		m.busy = false
		return m, nil
	case doctorLoadedMsg:
		m.doctor = msg.Doctor
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
		return m, tea.Batch(m.loadStatus(), m.loadDoctor())
	case operationFailedMsg:
		m.busy = false
		m.err = msg.Err
		m.overlay = overlayError
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	if key.Matches(msg, m.keys.SelectDown) {
		if m.cursor+1 < len(m.status.Services) {
			m.cursor++
		}
		return m, nil
	}
	if key.Matches(msg, m.keys.SelectUp) {
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	}
	if m.busy {
		return m, nil
	}
	if key.Matches(msg, m.keys.Refresh) {
		m.busy = true
		m.pending = append(m.pending, "refresh")
		return m, tea.Batch(m.loadStatus(), m.loadDoctor())
	}
	if key.Matches(msg, m.keys.Plan) {
		m.busy = true
		m.confirm = ""
		m.pending = append(m.pending, "plan")
		return m, m.loadPlan()
	}
	if key.Matches(msg, m.keys.Up) {
		m.pending = append(m.pending, "up")
		m.confirm = "up"
		m.busy = true
		return m, m.loadPlan()
	}
	if key.Matches(msg, m.keys.Down) {
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
	}
	m.busy = false
	m.confirm = ""
	return m, nil
}
