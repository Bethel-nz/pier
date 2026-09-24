//go:build e2e && windows

package e2e

import (
	"os/exec"
	"strconv"
	"syscall"
	"testing"
)

// startGroup gives pier up its own console process group, so Ctrl-Break can
// reach it without reaching the test.
func startGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// interrupt sends Ctrl-Break, which Go delivers as os.Interrupt. A runner
// without a console cannot, so then the whole tree is ended instead.
func interrupt(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	generate := syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")
	const ctrlBreak = 1
	if ok, _, err := generate.Call(ctrlBreak, uintptr(cmd.Process.Pid)); ok == 0 {
		t.Logf("no console for Ctrl-Break (%v); ending the process tree", err)
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	}
}
