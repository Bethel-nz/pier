package tailscale

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

const executable = "tailscale"

// Runner is the single process-execution boundary used by the Tailscale client.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// ExecRunner executes commands with the operating system process API.
type ExecRunner struct{}

// Run executes one command and captures stdout and stderr independently.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func versionCommand() (string, []string) {
	return executable, []string{"version"}
}

func nodeStatusCommand() (string, []string) {
	return executable, []string{"status", "--json"}
}

func funnelStatusCommand() (string, []string) {
	return executable, []string{"funnel", "status", "--json"}
}

// UpArgs returns the Tailscale argv that publishes one HTTPS route.
func UpArgs(route Route) ([]string, error) {
	if err := validateFunnelPort(route); err != nil {
		return nil, err
	}
	return []string{
		serveCommand(route.Public),
		"--bg",
		"--yes",
		httpsFlag(route.HTTPSPort),
		setPathFlag(route.Path),
		route.Target,
	}, nil
}

// DownArgs returns the Tailscale argv that removes one path-specific HTTPS route.
func DownArgs(route Route) ([]string, error) {
	if err := validateFunnelPort(route); err != nil {
		return nil, err
	}
	return []string{
		serveCommand(route.Public),
		httpsFlag(route.HTTPSPort),
		setPathFlag(route.Path),
		"off",
	}, nil
}

func serveCommand(public bool) string {
	if public {
		return "funnel"
	}
	return "serve"
}

func httpsFlag(port uint16) string {
	return fmt.Sprintf("--https=%d", port)
}

func setPathFlag(path string) string {
	return "--set-path=" + path
}

func validateFunnelPort(route Route) error {
	if !route.Public {
		return nil
	}
	switch route.HTTPSPort {
	case 443, 8443, 10000:
		return nil
	default:
		return fmt.Errorf("public Tailscale routes cannot use HTTPS port %d; Funnel allows only 443, 8443, and 10000", route.HTTPSPort)
	}
}
