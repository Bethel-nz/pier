//go:build linux

package localname

import (
	"errors"
	"os"
	"path/filepath"
)

// setLoginItem writes or removes a systemd user unit and enables it by
// linking it into default.target.wants, which is what `systemctl --user
// enable` does, without needing a running user bus.
func setLoginItem(exe string, on bool) error {
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	unit := filepath.Join(config, "systemd", "user", systemdUnitName)
	link := filepath.Join(config, "systemd", "user", "default.target.wants", systemdUnitName)
	if !on {
		if err := removeIfExists(link); err != nil {
			return err
		}
		return removeIfExists(unit)
	}
	if err := writeIfChanged(unit, []byte(systemdUnit(exe))); err != nil {
		return err
	}
	if target, err := os.Readlink(link); err == nil && target == unit {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Symlink(unit, link)
}
