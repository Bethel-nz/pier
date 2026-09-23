//go:build !darwin && !linux && !windows

package localname

type daemonLock struct{}

func (l *daemonLock) Close() error { return nil }

func lockDaemon() (*daemonLock, bool, error) {
	return &daemonLock{}, true, nil
}
