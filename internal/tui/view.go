package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"pier/internal/app"
	"pier/internal/health"
)

func (m model) View() string {
	if m.quit {
		return ""
	}
	header := fmt.Sprintf("PIER  %s                                  Tailscale: %s", projectName(m), m.connected())
	if m.busy {
		header += "  working"
	}
	body := m.serviceTable()
	summary := m.summaryLine()
	help := "↑/↓ select   u up   d down   p plan   s share/unshare\nc copy       o open r refresh         ? help   q quit"
	screen := strings.Join([]string{header, "", body, "", summary, "", help}, "\n")
	switch m.overlay {
	case overlayHelp:
		return screen + "\n\n" + m.helpOverlay()
	case overlayPlan:
		return screen + "\n\n" + m.planOverlay()
	case overlayConfirm:
		return screen + "\n\n" + m.confirmOverlay()
	case overlayError:
		return screen + "\n\n" + m.errorOverlay()
	}
	return screen
}

func projectName(m model) string {
	if m.status.Project.Root != "" {
		parts := strings.Split(strings.TrimSuffix(m.status.Project.Root, "/"), "/")
		if len(parts) > 0 && parts[len(parts)-1] != "" {
			return parts[len(parts)-1]
		}
	}
	if m.project.Root != "" {
		parts := strings.Split(strings.TrimSuffix(m.project.Root, "/"), "/")
		return parts[len(parts)-1]
	}
	return "pier"
}

func (m model) serviceTable() string {
	if len(m.status.Services) == 0 {
		return "no services"
	}
	if m.compact() {
		var b strings.Builder
		for i, service := range m.status.Services {
			marker := " "
			if i == m.cursor {
				marker = ">"
			}
			fmt.Fprintf(&b, "%s %s\n  %s  %s  %s  %s\n  %s\n",
				marker, service.Name, displayTarget(service), service.Path, visibility(service), healthLabel(service), service.URL)
		}
		return strings.TrimRight(b.String(), "\n")
	}
	var b strings.Builder
	fmt.Fprintln(&b, "SERVICE   TARGET           PATH    PUBLIC  HEALTH      URL")
	for i, service := range m.status.Services {
		marker := " "
		if i == m.cursor {
			marker = ">"
		}
		fmt.Fprintf(&b, "%s%-8s %-16s %-7s %-7s %-11s %s\n",
			marker,
			service.Name,
			displayTarget(service),
			service.Path,
			visibility(service),
			healthLabel(service),
			service.URL,
		)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m model) summaryLine() string {
	total := len(m.status.Services)
	public := 0
	for _, service := range m.status.Services {
		if service.Public {
			public++
		}
	}
	return fmt.Sprintf("%d services · %d tailnet · %d public", total, total-public, public)
}

func (m model) helpOverlay() string {
	return "help\n↑/↓ select   u up   d down   p plan   s share/unshare\nc copy       o open r refresh         ? help   q quit\nesc dismiss"
}

func (m model) planOverlay() string {
	var b strings.Builder
	fmt.Fprintln(&b, "plan")
	if len(m.plan.Plan.Operations) == 0 {
		fmt.Fprintln(&b, "no changes")
	}
	for _, op := range m.plan.Plan.Operations {
		fmt.Fprintf(&b, "%s  %s\n", op.Kind, op.After.Key())
	}
	return b.String()
}

func (m model) confirmOverlay() string {
	return fmt.Sprintf("confirm %s?\nenter confirm   esc cancel", m.confirm)
}

func (m model) errorOverlay() string {
	if m.err == nil {
		return "error"
	}
	return "error\n" + m.err.Error() + "\npress any key to dismiss"
}

func visibility(service app.ServiceInfo) string {
	if service.Public {
		return "public"
	}
	return "tailnet"
}

func healthLabel(service app.ServiceInfo) string {
	switch service.Health.Status {
	case health.StatusHealthy:
		return "healthy"
	case health.StatusUnavailable:
		return "unavailable"
	default:
		if service.Health.Status == "" {
			return "unavailable"
		}
		return string(service.Health.Status)
	}
}

func displayTarget(service app.ServiceInfo) string {
	if service.Host != "" && service.Port != 0 {
		return service.Host + ":" + strconv.FormatUint(uint64(service.Port), 10)
	}
	return service.Target
}

func colorEnabled(noColor bool) bool {
	if noColor {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return true
}

var _ = lipgloss.NewStyle
var _ = colorEnabled
