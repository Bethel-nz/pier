//go:build !darwin && !linux && !windows

package localname

import "fmt"

func ensureDaemon() error {
	return fmt.Errorf("local domains are not supported on this operating system")
}

func stopDaemon() error { return nil }
