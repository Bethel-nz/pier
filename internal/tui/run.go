package tui

import (
	"context"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"pier/internal/app"
	"pier/internal/project"
)

// Run starts the interactive management interface.
func Run(ctx context.Context, svc *app.Service, proj project.Context, options Options) error {
	m := newModel(ctx, svc, proj, options)
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithAltScreen())
	_, err := program.Run()
	return err
}

// StdioIsTTY reports whether stdin and stdout are character devices.
func StdioIsTTY() bool {
	statIn, errIn := os.Stdin.Stat()
	statOut, errOut := os.Stdout.Stat()
	if errIn != nil || errOut != nil {
		return false
	}
	return statIn.Mode()&os.ModeCharDevice != 0 && statOut.Mode()&os.ModeCharDevice != 0
}
