//go:build e2e && !windows

package e2e

import (
	"os"
	"os/exec"
	"testing"
)

func startGroup(*exec.Cmd) {}

// interrupt is Ctrl-C.
func interrupt(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
}
