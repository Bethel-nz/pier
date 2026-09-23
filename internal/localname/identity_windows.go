//go:build windows

package localname

import (
	"strings"
	"syscall"
	"unsafe"
)

func processImage(pid int) string {
	const processQueryLimited = 0x1000
	kernel := syscall.NewLazyDLL("kernel32.dll")
	openProcess := kernel.NewProc("OpenProcess")
	query := kernel.NewProc("QueryFullProcessImageNameW")
	closeHandle := kernel.NewProc("CloseHandle")
	handle, _, _ := openProcess.Call(processQueryLimited, 0, uintptr(pid))
	if handle == 0 {
		return ""
	}
	defer closeHandle.Call(handle)
	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	ok, _, _ := query.Call(handle, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:size])
}

func isLocald(pid int) bool {
	return strings.Contains(strings.ToLower(processImage(pid)), "pier")
}

func isAdvertiser(int) bool { return false }
