//go:build darwin || linux

package localname

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func ensureDaemon() error {
	if daemonAlive() {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("Pier could not start local name publishing: %w", err)
	}
	command := exec.Command(exe, "locald")
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	if err := command.Start(); err != nil {
		return fmt.Errorf("Pier could not start local name publishing: %w", err)
	}
	if err := writePID(command.Process.Pid); err != nil {
		return err
	}
	return WaitReady()
}

func stopDaemon() error {
	pid, ok := readPID()
	if !ok {
		return nil
	}
	if daemonAlive() && isLocald(pid) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	path, err := pidPath()
	if err != nil {
		return err
	}
	_ = os.Remove(path)
	return nil
}

func daemonAlive() bool {
	pid, ok := readPID()
	if !ok {
		return false
	}
	return syscall.Kill(pid, 0) == nil && isLocald(pid)
}

func writePID(pid int) error {
	path, err := pidPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o600)
}

func readPID() (int, bool) {
	path, err := pidPath()
	if err != nil {
		return 0, false
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}
