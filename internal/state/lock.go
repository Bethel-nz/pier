package state

import (
	"os"
	"path/filepath"
)

// Lock holds projectID's change lock until unlock is called. Every command
// that changes the project's routes holds it from applying to saving, so
// Pier's background check never acts on routes caught halfway through a
// change, such as a public route pier unshare just removed.
func (s *Store) Lock(projectID string) (unlock func(), err error) {
	unlock, _, err = s.lock(projectID, true)
	return unlock, err
}

// TryLock is Lock without waiting: ok is false while another holds it.
func (s *Store) TryLock(projectID string) (unlock func(), ok bool, err error) {
	return s.lock(projectID, false)
}

func (s *Store) lock(projectID string, wait bool) (func(), bool, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, false, err
	}
	path := filepath.Join(s.dir, "."+fileID(projectID)+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	ok, err := lockFile(file, wait)
	if err != nil || !ok {
		_ = file.Close()
		return nil, false, err
	}
	return func() {
		unlockFile(file)
		_ = file.Close()
	}, true, nil
}
