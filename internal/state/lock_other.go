//go:build !darwin && !linux && !windows

package state

import "os"

func lockFile(*os.File, bool) (bool, error) { return true, nil }

func unlockFile(*os.File) {}
