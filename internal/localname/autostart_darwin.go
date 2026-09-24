//go:build darwin

package localname

import (
	"os"
	"path/filepath"
)

// setLoginItem writes or removes ~/Library/LaunchAgents/dev.pier.locald.plist.
// launchd reads it at the next login; the daemon pier up started keeps running now.
func setLoginItem(exe string, on bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
	if !on {
		return removeIfExists(path)
	}
	logFile, err := logPath()
	if err != nil {
		return err
	}
	return writeIfChanged(path, []byte(launchdPlist(exe, logFile)))
}
