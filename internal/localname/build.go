package localname

import (
	"fmt"
	"os"
)

// buildID identifies the pier binary so an upgrade restarts an older daemon.
func buildID() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	info, err := os.Stat(exe)
	if err != nil {
		return exe
	}
	return fmt.Sprintf("%s:%d:%d", exe, info.Size(), info.ModTime().UnixNano())
}
