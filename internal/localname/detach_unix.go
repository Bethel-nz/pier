//go:build !windows

package localname

import (
	"os"
	"os/exec"
	"syscall"
)

// detach starts the daemon in its own session so it outlives the terminal.
func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// askToStop asks a process to shut down cleanly.
func askToStop(process *os.Process) error { return process.Signal(syscall.SIGTERM) }
