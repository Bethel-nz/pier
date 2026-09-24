//go:build windows

package mdns

import "syscall"

// reuseControl lets Pier share UDP 5353 with the Windows DNS client.
func reuseControl(_, _ string, raw syscall.RawConn) error {
	var sockErr error
	err := raw.Control(func(fd uintptr) {
		sockErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	})
	if err != nil {
		return err
	}
	return sockErr
}
