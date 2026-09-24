//go:build !windows

package mdns

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// reuseControl lets Pier share UDP 5353 with the system's own responder.
func reuseControl(_, _ string, raw syscall.RawConn) error {
	var sockErr error
	err := raw.Control(func(fd uintptr) {
		sockErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		if sockErr == nil {
			sockErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		}
	})
	if err != nil {
		return err
	}
	return sockErr
}
