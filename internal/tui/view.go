package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/health"
	"github.com/Bethel-nz/pier/internal/state"
)

var (
	accentColor = lipgloss.Color("79")
	goodColor   = lipgloss.Color("42")
	warnColor   = lipgloss.Color("214")
	mutedColor  = lipgloss.Color("245")
)

func (m model) View() string {
	if m.quit {
		return ""
	}

	var content string
	if m.compact() {
		content = m.compactView()
	} else {
		content = m.wideView()
	}
	screen := strings.Join([]string{m.header(), content, m.footer()}, "\n")

	var modal string
	switch m.overlay {
	case overlayHelp:
		modal = m.helpOverlay()
	case overlayPlan:
		modal = m.planOverlay()
	case overlayConfirm:
		modal = m.confirmOverlay()
	case overlayError:
		modal = m.errorOverlay()
	case overlayAdd:
		modal = m.addOverlay()
	case overlayAddService:
		modal = m.addServiceOverlay()
	}
	if modal == "" {
		return screen
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.modalStyle().Render(modal))
}

func (m model) header() string {
	name := projectName(m)
	if !m.hasProject() {
		name = "Projects"
	}
	left := m.style(lipgloss.NewStyle().Bold(true).Foreground(accentColor)).Render(iconPier+" PIER") + "  " + name
	right := "Tailscale  " + m.connectionLabel()
	if m.busy {
		right += "  " + iconChecking + " working"
	}
	space := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", space) + right
}

func (m model) footer() string {
	var help string
	switch m.focus {
	case focusProjects:
		help = "↑/↓ select  enter open  a add  tab next  ? help  q quit"
	case focusActivity:
		help = "↑/↓ scroll  r refresh  tab next  ? help  q quit"
	default:
		help = "↑/↓ service  enter open  space pause  a add  u up  d down  s share  p plan  tab next  ? help  q quit"
	}
	return m.style(lipgloss.NewStyle().Foreground(mutedColor)).Render(truncate(help, m.width))
}

func (m model) wideView() string {
	leftWidth, projectWidth, activityWidth := m.widePanelWidths()
	projectHeight, activityHeight := m.widePanelHeights()
	left := m.panel(iconProjects+" PROJECTS", m.projectsView(leftWidth), leftWidth, projectHeight+activityHeight, m.focus == focusProjects)
	project := m.panel(iconCurrentProject+" "+projectName(m), m.dashboardView(projectWidth-4), projectWidth, projectHeight, m.focus == focusServices)
	activityTitle := iconActivity + " ACTIVITY"
	if m.hasProject() {
		activityTitle += " · " + projectName(m)
	}
	activity := m.panel(activityTitle, m.activityView(), activityWidth, activityHeight, m.focus == focusActivity)
	right := lipgloss.JoinVertical(lipgloss.Left, project, activity)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m model) widePanelWidths() (projects, project, activity int) {
	projects = min(32, max(24, m.width/4))
	project = max(48, m.width-projects-1)
	activity = project
	return projects, project, activity
}

func (m model) widePanelHeights() (project, activity int) {
	height := max(14, m.height-2)
	activity = min(12, max(8, height/3))
	project = height - activity
	return project, activity
}

func (m model) compactView() string {
	height := max(10, m.height-2)
	switch m.focus {
	case focusProjects:
		return m.panel(iconProjects+" PROJECTS", m.projectsView(m.width), m.width, height, true)
	case focusActivity:
		return m.panel(iconActivity+" ACTIVITY", m.activityView(), m.width, height, true)
	default:
		return m.panel(iconCurrentProject+" "+projectName(m), m.dashboardView(m.width-4), m.width, height, true)
	}
}

func (m model) projectsView(width int) string {
	var b strings.Builder
	if len(m.projects) == 0 {
		fmt.Fprintln(&b, m.style(lipgloss.NewStyle().Foreground(mutedColor)).Render("No known projects yet."))
		fmt.Fprintln(&b)
	}
	for index, item := range m.projects {
		marker := "  "
		if index == m.projectCursor && m.focus == focusProjects {
			marker = "> "
		}
		indicator := iconProject
		if item.ProjectID == m.project.ID {
			indicator = m.style(lipgloss.NewStyle().Foreground(goodColor)).Render(iconCurrentProject)
		}
		if !projectAvailable(item) {
			indicator = m.style(lipgloss.NewStyle().Foreground(warnColor)).Render(iconMissing)
		}
		name := item.Name
		if name == "" {
			name = filepath.Base(item.Path)
		}
		row := fmt.Sprintf("%s%s %s", marker, indicator, name)
		if index == m.projectCursor && m.focus == focusProjects {
			row = m.style(lipgloss.NewStyle().Bold(true).Foreground(accentColor)).Render(row)
		}
		fmt.Fprintln(&b, row)
		fmt.Fprintf(&b, "    %s\n", m.style(lipgloss.NewStyle().Foreground(mutedColor)).Render(truncate(item.Path, max(8, width-10))))
	}
	marker := "  "
	if m.projectCursor == len(m.projects) && m.focus == focusProjects {
		marker = "> "
	}
	label := iconAdd + " Add project"
	if !m.hasProject() {
		label = iconAdd + " Initialize here"
	}
	fmt.Fprintf(&b, "%s%s", marker, m.style(lipgloss.NewStyle().Bold(true).Foreground(accentColor)).Render(label))
	return strings.TrimRight(b.String(), "\n")
}

func (m model) dashboardView(width int) string {
	if !m.hasProject() {
		return strings.Join([]string{
			"Select a known project and press enter.",
			"",
			"Or initialize the current directory to create:",
			m.style(lipgloss.NewStyle().Foreground(accentColor)).Render("  pier.yaml"),
		}, "\n")
	}

	meta := fmt.Sprintf("%s Config     %s\n  Tailscale  %s", iconConfig, truncate(m.project.ConfigPath, max(12, width-13)), m.connectionLabel())
	sections := []string{meta, "", m.sectionTitle(iconServices+" SERVICES", focusServices), m.serviceTable(width)}
	if service, ok := m.selected(); ok {
		sections = append(sections, "", m.serviceDetails(service))
	}
	return strings.Join(sections, "\n")
}

func (m model) activityView() string {
	if !m.hasProject() {
		return m.style(lipgloss.NewStyle().Foreground(mutedColor)).Render("Select a project to view its activity.")
	}
	if strings.TrimSpace(m.activityLog.String()) == "" {
		return m.style(lipgloss.NewStyle().Foreground(mutedColor)).Render("Waiting for Pier activity…")
	}
	return m.activity.View()
}

func (m model) sectionTitle(title string, pane focusPane) string {
	if m.focus == pane {
		return m.style(lipgloss.NewStyle().Bold(true).Foreground(accentColor)).Render(title)
	}
	return title
}

func (m model) serviceTable(width int) string {
	if len(m.status.Services) == 0 {
		if m.busy {
			return "Loading services…"
		}
		return "No configured services.\nPress a to add one."
	}
	if m.compact() || width < 64 {
		var b strings.Builder
		for i, service := range m.status.Services {
			marker := " "
			if i == m.cursor {
				marker = ">"
			}
			row := fmt.Sprintf("%s %s  %s", marker, service.Name, healthLabel(service))
			if i == m.cursor {
				row = m.style(lipgloss.NewStyle().Bold(true).Foreground(accentColor)).Render(row)
			}
			fmt.Fprintf(&b, "%s\n  %s  %s  %s\n", row, displayTarget(service), visibility(service), service.URL)
		}
		return strings.TrimRight(b.String(), "\n")
	}
	var b strings.Builder
	fmt.Fprintln(&b, "SERVICE     TARGET              ACCESS    HEALTH")
	for i, service := range m.status.Services {
		marker := " "
		if i == m.cursor {
			marker = ">"
		}
		row := fmt.Sprintf("%s %-10s %-19s %-11s %s",
			marker, service.Name, truncate(displayTarget(service), 19), visibility(service), healthLabel(service))
		if i == m.cursor {
			row = m.style(lipgloss.NewStyle().Bold(true).Foreground(accentColor)).Render(row)
		}
		fmt.Fprintln(&b, row)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m model) serviceDetails(service app.ServiceInfo) string {
	route := "active"
	if service.Paused {
		route = "paused"
	}
	return fmt.Sprintf("Selected  %s\n%s Route    %s  %s\n  URL      %s", service.Name, iconRoute, service.Path, route, service.URL)
}

func (m model) connectionLabel() string {
	switch m.connected() {
	case "connected":
		return m.style(lipgloss.NewStyle().Bold(true).Foreground(goodColor)).Render(iconConnected + " connected")
	case "unavailable":
		return m.style(lipgloss.NewStyle().Bold(true).Foreground(warnColor)).Render(iconUnavailable + " unavailable")
	default:
		return m.style(lipgloss.NewStyle().Foreground(mutedColor)).Render(iconChecking + " checking")
	}
}

func projectName(m model) string {
	for _, item := range m.projects {
		if item.ProjectID == m.project.ID && item.Name != "" {
			return item.Name
		}
	}
	if m.status.Project.Root != "" {
		return filepath.Base(m.status.Project.Root)
	}
	if m.project.Root != "" {
		return filepath.Base(m.project.Root)
	}
	return "Pier"
}

func (m model) helpOverlay() string {
	return "Keyboard\n\n↑/k  previous       ↓/j  next\nh/l  switch pane    tab  next pane\nenter open/select   a    add project/service\nspace pause/resume  u    expose routes\nd    remove owned   p    preview plan\ns    share/unshare  c    copy URL\no    open URL       r    refresh\nq    quit\n\nesc dismiss"
}

func (m model) planOverlay() string {
	var b strings.Builder
	fmt.Fprintln(&b, "Plan")
	fmt.Fprintln(&b)
	if len(m.plan.Plan.Operations) == 0 {
		fmt.Fprintln(&b, "No changes.")
	}
	for _, op := range m.plan.Plan.Operations {
		fmt.Fprintf(&b, "%s  %s\n", op.Kind, op.After.Key())
	}
	fmt.Fprintln(&b, "\nesc dismiss")
	return strings.TrimRight(b.String(), "\n")
}

func (m model) confirmOverlay() string {
	return fmt.Sprintf("Confirm %s?\n\nenter/y confirm   esc/n cancel", m.confirm)
}

func (m model) errorOverlay() string {
	if m.err == nil {
		return "Error"
	}
	return "Error\n\n" + m.err.Error() + "\n\npress any key to dismiss"
}

func (m model) addOverlay() string {
	return "Add or initialize project\n\n" + m.pathInput.View() +
		"\n\nIf pier.yaml is missing, Pier creates a safe default.\nenter confirm   esc cancel"
}

func (m model) addServiceOverlay() string {
	if m.form == nil {
		return "Add service"
	}
	return m.form.View()
}

func (m model) panel(title, body string, width, height int, focused bool) string {
	border := mutedColor
	if focused {
		border = accentColor
	}
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Width(max(1, width-2)).Height(max(1, height-2)).Padding(0, 1)
	if !colorEnabled(m.disableColor) {
		style = style.BorderForeground(lipgloss.NoColor{})
	}
	titleStyle := m.style(lipgloss.NewStyle().Bold(true).Foreground(border))
	return style.Render(titleStyle.Render(title) + "\n\n" + body)
}

func (m model) modalStyle() lipgloss.Style {
	style := lipgloss.NewStyle().Border(lipgloss.DoubleBorder()).BorderForeground(accentColor).
		Padding(1, 2).Width(min(68, max(28, m.width-8)))
	if !colorEnabled(m.disableColor) {
		style = style.BorderForeground(lipgloss.NoColor{})
	}
	return style
}

func (m model) style(style lipgloss.Style) lipgloss.Style {
	if !colorEnabled(m.disableColor) {
		return style.Foreground(lipgloss.NoColor{}).Background(lipgloss.NoColor{})
	}
	return style
}

func projectAvailable(item state.ProjectState) bool {
	if item.Path == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(item.Path, "pier.yaml"))
	return err == nil && !info.IsDir()
}

func visibility(service app.ServiceInfo) string {
	if service.Paused {
		return iconPaused + " paused"
	}
	if service.Public {
		return iconPublic + " public"
	}
	return iconTailnet + " tailnet"
}

func healthLabel(service app.ServiceInfo) string {
	switch service.Health.Status {
	case health.StatusHealthy:
		return iconHealthy + " healthy"
	case health.StatusUnavailable:
		return iconUnavailable + " unavailable"
	default:
		if service.Health.Status == "" {
			return iconChecking + " unknown"
		}
		return iconChecking + " " + string(service.Health.Status)
	}
}

func displayTarget(service app.ServiceInfo) string {
	if service.Host != "" && service.Port != 0 {
		return service.Host + ":" + strconv.FormatUint(uint64(service.Port), 10)
	}
	return service.Target
}

func colorEnabled(noColor bool) bool {
	return !noColor && os.Getenv("NO_COLOR") == ""
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(value)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}
