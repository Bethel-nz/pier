//go:build !darwin && !linux && !windows

package localname

import (
	"context"
	"fmt"
	"net"
)

// DNSAnnouncer is unavailable where dns-sd is not the local name publisher.
type DNSAnnouncer struct{}

// NewAnnouncer reports that this system has no local name publisher.
func NewAnnouncer() Announcer { return &DNSAnnouncer{} }

// OSProcesses reports that nothing is published.
func (a *DNSAnnouncer) OSProcesses() bool { return false }

// Start reports that this system has no local name publisher.
func (a *DNSAnnouncer) Start(context.Context, Record, net.IP) (int, error) {
	return 0, fmt.Errorf("local domains are not supported on this operating system")
}

// Stop reports that local names are published with dns-sd on macOS.
func (a *DNSAnnouncer) Stop(int) error { return nil }

func stopPID(int) error { return nil }
