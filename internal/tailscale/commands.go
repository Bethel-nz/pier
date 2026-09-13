package tailscale

import (
	"bytes"
	"context"
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
