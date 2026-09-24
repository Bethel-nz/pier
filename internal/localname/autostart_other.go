//go:build !darwin && !linux && !windows

package localname

import "errors"

func setLoginItem(_ string, on bool) error {
	if on {
		return errors.New("autostart is not supported on this operating system")
	}
	return nil
}
