//go:build windows

package localname

import (
	"syscall"
	"unsafe"
)

type daemonLock struct {
	handle syscall.Handle
}

func (l *daemonLock) Close() error {
	if l == nil || l.handle == 0 {
		return nil
	}
	return syscall.CloseHandle(l.handle)
}

func lockDaemon() (*daemonLock, bool, error) {
	name, err := syscall.UTF16PtrFromString(`Local\PierLocalNames`)
	if err != nil {
		return nil, false, err
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	create := kernel.NewProc("CreateMutexW")
	handle, _, callErr := create.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return nil, false, callErr
	}
	if callErr == syscall.ERROR_ALREADY_EXISTS {
		_ = syscall.CloseHandle(syscall.Handle(handle))
		return nil, false, nil
	}
	return &daemonLock{handle: syscall.Handle(handle)}, true, nil
}
