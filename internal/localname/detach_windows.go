//go:build windows

package localname

import (
	"os"
	"os/exec"
	"syscall"
)

const (
	createNoWindow        = 0x08000000
	createNewProcessGroup = 0x00000200
)

// detach starts the daemon without a console so it outlives the terminal.
func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow | createNewProcessGroup,
		HideWindow:    true,
	}
}

// askToStop stops a process. Windows has no SIGTERM for a console process
// without a window, so it is killed.
func askToStop(process *os.Process) error { return process.Kill() }
