//go:build !windows

package localname

import (
	"os/exec"
	"syscall"
)

// detach starts the daemon in its own session so it outlives the terminal.
func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
