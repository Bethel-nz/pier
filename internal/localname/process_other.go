//go:build !darwin

package localname

import (
	"context"
	"fmt"
)

// DNSAnnouncer is unavailable where dns-sd is not the local name publisher.
type DNSAnnouncer struct{}

// Start reports that local names are published with dns-sd on macOS.
func (a *DNSAnnouncer) Start(context.Context, []string) (int, error) {
	return 0, fmt.Errorf("local domains are published with dns-sd on macOS")
}

// Stop reports that local names are published with dns-sd on macOS.
func (a *DNSAnnouncer) Stop(int) error { return nil }

func stopPID(int) error { return nil }
