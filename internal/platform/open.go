package platform

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Runner executes a platform command. stdin is optional process input.
type Runner func(ctx context.Context, stdin, name string, args ...string) error

// LookPath resolves an executable name, like exec.LookPath.
type LookPath func(file string) (string, error)

// Actions opens URLs and copies text using OS-specific commands.
type Actions struct {
	GOOS     string
	Run      Runner
	LookPath LookPath
}

func (a Actions) goos() string {
	if a.GOOS != "" {
		return a.GOOS
	}
	return runtime.GOOS
}

func (a Actions) run() Runner {
	if a.Run != nil {
		return a.Run
	}
	return execRun
}

func (a Actions) lookPath() LookPath {
	if a.LookPath != nil {
		return a.LookPath
	}
	return exec.LookPath
}

// Open opens url with the platform browser helper.
func Open(ctx context.Context, url string) error {
	return (Actions{}).Open(ctx, url)
}

// Open opens url with the injected or default runner.
func (a Actions) Open(ctx context.Context, url string) error {
	name, args, err := openCommand(a.goos(), url)
	if err != nil {
		return err
	}
	if err := a.run()(ctx, "", name, args...); err != nil {
		return fmt.Errorf("Pier could not open %s: %w", url, err)
	}
	return nil
}

func openCommand(goos, url string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "open", []string{url}, nil
	case "linux":
		return "xdg-open", []string{url}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}, nil
	default:
		return "", nil, fmt.Errorf("Pier does not support opening URLs on %s", goos)
	}
}

func execRun(ctx context.Context, stdin, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	return cmd.Run()
}
