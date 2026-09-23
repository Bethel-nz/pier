//go:build linux

package localname

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
)

// DNSAnnouncer publishes names through Avahi.
type DNSAnnouncer struct {
	commands map[int]*exec.Cmd
}

// NewAnnouncer publishes names through Avahi.
func NewAnnouncer() Announcer { return &DNSAnnouncer{} }

// OSProcesses reports that avahi-publish is a real process.
func (a *DNSAnnouncer) OSProcesses() bool { return true }

// Start runs avahi-publish for one address record and returns its process id.
func (a *DNSAnnouncer) Start(ctx context.Context, record Record, ip net.IP) (int, error) {
	command := exec.CommandContext(ctx, "avahi-publish", AvahiArgs(record.Name, ip)...)
	if err := command.Start(); err != nil {
		return 0, fmt.Errorf("Pier could not publish %s. Install avahi-utils and start avahi-daemon: %w", record.Name, err)
	}
	if a.commands == nil {
		a.commands = map[int]*exec.Cmd{}
	}
	a.commands[command.Process.Pid] = command
	return command.Process.Pid, nil
}

// Stop ends one avahi-publish process.
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
