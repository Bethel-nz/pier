//go:build darwin

package localname

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// DNSAnnouncer publishes names with dns-sd and stops those processes by pid.
type DNSAnnouncer struct {
	commands map[int]*exec.Cmd
}

// Start runs dns-sd with args and returns its process id.
func (a *DNSAnnouncer) Start(ctx context.Context, args []string) (int, error) {
	command := exec.CommandContext(ctx, "dns-sd", args...)
	if err := command.Start(); err != nil {
		return 0, err
	}
	if a.commands == nil {
		a.commands = map[int]*exec.Cmd{}
	}
	a.commands[command.Process.Pid] = command
	return command.Process.Pid, nil
}

// Stop ends one dns-sd process.
func (a *DNSAnnouncer) Stop(pid int) error {
	if command, ok := a.commands[pid]; ok {
		delete(a.commands, pid)
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
		return nil
	}
	return stopPID(pid)
}

func stopPID(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	return process.Signal(syscall.SIGTERM)
}
