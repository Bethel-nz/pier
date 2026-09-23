//go:build windows

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

const (
	createNoWindow        = 0x08000000
	createNewProcessGroup = 0x00000200
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
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow | createNewProcessGroup,
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("Pier could not start local name publishing: %w", err)
	}
	return writePID(command.Process.Pid)
}

func stopDaemon() error {
	pid, ok := readPID()
	if !ok {
		return nil
	}
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Kill()
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
	const processQueryLimited = 0x1000
	kernel := syscall.NewLazyDLL("kernel32.dll")
	openProcess := kernel.NewProc("OpenProcess")
	closeHandle := kernel.NewProc("CloseHandle")
	handle, _, _ := openProcess.Call(processQueryLimited, 0, uintptr(pid))
	if handle == 0 {
		return false
	}
	_, _, _ = closeHandle.Call(handle)
	return true
}

func pidPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "pier", "locald.pid"), nil
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
