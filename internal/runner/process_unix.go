//go:build !windows

package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// shellCommand runs line through sh in its own process group, so stopping it
// also stops whatever it started (bun, node, go run's child).
func shellCommand(line string) *exec.Cmd {
	cmd := exec.Command("/bin/sh", "-c", line)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

func terminate(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }

func kill(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

// Lock is held while this pier up runs the project's commands.
type Lock struct{ file *os.File }

// Acquire takes the project's run lock in dir. ok is false when another pier
// up already runs this project.
func Acquire(dir string) (*Lock, bool, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, false, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "run.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &Lock{file: file}, true, nil
}

// Release lets another pier up run the project.
func (l *Lock) Release() {
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
}
