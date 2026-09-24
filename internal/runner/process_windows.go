//go:build windows

package runner

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// shellCommand runs line through cmd.exe in a new process group.
func shellCommand(line string) *exec.Cmd {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       "/d /s /c \"" + line + "\"",
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	return cmd
}

// terminate asks the whole tree to close; console servers that ignore it are
// killed after stopGrace.
func terminate(cmd *exec.Cmd) {
	_ = exec.Command("taskkill", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}

func kill(cmd *exec.Cmd) {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}

const errSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION

// Lock is held while this pier up runs the project's commands.
type Lock struct{ handle syscall.Handle }

// Acquire opens run.lock without sharing, so a second pier up cannot.
func Acquire(dir string) (*Lock, bool, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false, err
	}
	name, err := syscall.UTF16PtrFromString(filepath.Join(dir, "run.lock"))
	if err != nil {
		return nil, false, err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if errors.Is(err, errSharingViolation) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &Lock{handle: handle}, true, nil
}

// Release lets another pier up run the project.
func (l *Lock) Release() { _ = syscall.CloseHandle(l.handle) }
