package tui

import (
	"strings"
	"sync"

	charmlog "github.com/charmbracelet/log"
)

type activityWriter struct {
	mu      sync.Mutex
	lines   []string
	partial string
	limit   int
}

func newActivityLog() (*activityWriter, *charmlog.Logger) {
	writer := &activityWriter{limit: 200}
	logger := charmlog.NewWithOptions(writer, charmlog.Options{
		ReportTimestamp: true,
		TimeFormat:      "15:04:05",
	})
	return writer, logger
}

func (w *activityWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	parts := strings.Split(w.partial+string(p), "\n")
	w.partial = parts[len(parts)-1]
	for _, line := range parts[:len(parts)-1] {
		if strings.TrimSpace(line) != "" {
			w.lines = append(w.lines, line)
		}
	}
	if len(w.lines) > w.limit {
		w.lines = append([]string(nil), w.lines[len(w.lines)-w.limit:]...)
	}
	return len(p), nil
}

func (w *activityWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	lines := append([]string(nil), w.lines...)
	if w.partial != "" {
		lines = append(lines, w.partial)
	}
	return strings.Join(lines, "\n")
}

func (w *activityWriter) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lines = nil
	w.partial = ""
}
