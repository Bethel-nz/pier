package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"

	"pier/internal/config"
)

// ErrFormAborted is returned when the user cancels a Huh form.
var ErrFormAborted = huh.ErrUserAborted

// AddServiceValues is the Huh form answer set for creating a service.
type AddServiceValues struct {
	Name     string
	Target   string
	Path     string
	Protocol string
	Public   bool
}

func newAddServiceForm(values *AddServiceValues, existing []string) *huh.Form {
	if values.Path == "" {
		values.Path = "/"
	}
	if values.Target == "" {
		values.Target = "localhost:3000"
	}
	if values.Protocol == "" {
		values.Protocol = string(config.ProtocolHTTP)
	}
	return huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Key("name").
				Title("Name").
				Placeholder("api").
				Value(&values.Name).
				Validate(validateServiceName(existing)),
			huh.NewInput().
				Key("target").
				Title("Target").
				Placeholder("localhost:4000").
				Value(&values.Target).
				Validate(huh.ValidateNotEmpty()),
			huh.NewInput().
				Key("path").
				Title("Path").
				Placeholder("/").
				Value(&values.Path),
			huh.NewSelect[string]().
				Key("protocol").
				Title("Protocol").
				Options(huh.NewOptions(string(config.ProtocolHTTP), string(config.ProtocolHTTPS))...).
				Value(&values.Protocol),
			huh.NewConfirm().
				Key("public").
				Title("Public").
				Description("Funnel on the internet, or Serve on the tailnet").
				Affirmative("public").
				Negative("tailnet").
				Value(&values.Public),
		).Title("Add service"),
	).WithShowHelp(true).WithTheme(formTheme())
}

func formTheme() *huh.Theme {
	if os.Getenv("NO_COLOR") != "" {
		return huh.ThemeBase()
	}

	t := huh.ThemeBase()
	normalFg := lipgloss.AdaptiveColor{Light: "235", Dark: "252"}
	errorColor := lipgloss.Color("203")

	t.Focused.Base = t.Focused.Base.BorderForeground(accentColor)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = t.Focused.Title.Foreground(accentColor).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(accentColor).Bold(true).MarginBottom(1)
	t.Focused.Directory = t.Focused.Directory.Foreground(accentColor)
	t.Focused.Description = t.Focused.Description.Foreground(mutedColor)
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(errorColor)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(errorColor)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(accentColor)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(accentColor)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(accentColor)
	t.Focused.Option = t.Focused.Option.Foreground(normalFg)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(accentColor)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(goodColor)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(goodColor).SetString("✓ ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(mutedColor).SetString("• ")
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(normalFg)
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(lipgloss.Color("0")).Background(accentColor).Bold(true)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(normalFg).Background(lipgloss.Color("237"))
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(goodColor)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(mutedColor)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(accentColor)

	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description
	return t
}

func validateServiceName(existing []string) func(string) error {
	taken := make(map[string]struct{}, len(existing))
	for _, name := range existing {
		taken[name] = struct{}{}
	}
	return func(name string) error {
		name = strings.TrimSpace(name)
		if name == "" {
			return errors.New("name is required")
		}
		if !config.ValidServiceName(name) {
			return errors.New("must match ^[a-z][a-z0-9-]*$")
		}
		if _, exists := taken[name]; exists {
			return fmt.Errorf("service %q already exists", name)
		}
		return nil
	}
}

// FillAddService runs a standalone Huh form and writes the answers into values.
func FillAddService(values *AddServiceValues, existing []string) error {
	return newAddServiceForm(values, existing).Run()
}
