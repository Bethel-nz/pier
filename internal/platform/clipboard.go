package platform

import (
	"context"
	"fmt"
)

// Copy copies value to the platform clipboard.
func Copy(ctx context.Context, value string) error {
	return (Actions{}).Copy(ctx, value)
}

// Copy copies value using the injected or default runner.
func (a Actions) Copy(ctx context.Context, value string) error {
	commands, err := copyCommands(a.goos(), a.lookPath())
	if err != nil {
		return err
	}
	var last error
	for _, command := range commands {
		if err := a.run()(ctx, value, command[0], command[1:]...); err == nil {
			return nil
		} else {
			last = err
		}
	}
	return fmt.Errorf("Pier could not copy to the clipboard: %w", last)
}

func copyCommands(goos string, lookPath LookPath) ([][]string, error) {
	switch goos {
	case "darwin":
		return [][]string{{"pbcopy"}}, nil
	case "windows":
		return [][]string{{"clip"}}, nil
	case "linux":
		var commands [][]string
		if _, err := lookPath("wl-copy"); err == nil {
			commands = append(commands, []string{"wl-copy"})
		}
		if _, err := lookPath("xclip"); err == nil {
			commands = append(commands, []string{"xclip", "-selection", "clipboard"})
		}
		if len(commands) == 0 {
			return [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}}, nil
		}
		return commands, nil
	default:
		return nil, fmt.Errorf("Pier does not support clipboard copy on %s", goos)
	}
}
