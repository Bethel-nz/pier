//go:build darwin || linux

package localname

import (
	"os"
	"syscall"
)

type daemonLock struct {
	file *os.File
}

func (l *daemonLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	return l.file.Close()
}

// lockDaemon holds the publisher lock for this process. owned is false when
// another publisher already holds it.
func lockDaemon() (*daemonLock, bool, error) {
	path, err := lockPath()
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return nil, false, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
		_ = file.Close()
		return nil, false, nil
	}
	if err != nil {
		_ = file.Close()
		return nil, false, err
	}
	return &daemonLock{file: file}, true, nil
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}
