// Package localname runs Pier's local-network daemon and keeps it matched to
// saved project state: a .local name per service, served over HTTPS on the LAN.
package localname

import (
	"os"
	"path/filepath"
)

func runtimePath(name string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "pier", name), nil
}

func heartbeatPath() (string, error) { return runtimePath("pierd.json") }
func stopPath() (string, error)      { return runtimePath("pierd.stop") }
func lockPath() (string, error)      { return runtimePath("pierd.lock") }
func logPath() (string, error)       { return runtimePath("pierd.log") }

func dirOf(path string) string { return filepath.Dir(path) }
