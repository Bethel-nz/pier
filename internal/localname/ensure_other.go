//go:build !darwin

package localname

import "fmt"

func ensureDaemon() error {
	return fmt.Errorf("local domains are published with dns-sd on macOS")
}

func stopDaemon() error { return nil }
