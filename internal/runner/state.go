package runner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// State is what one run: command is doing, as pier status shows it.
type State string

const (
	StateRunning    State = "running"
	StateRestarting State = "restarting"
	// StateCrashed is a command that failed and that Pier no longer restarts:
	// it crashed too often, or its service says restart: never.
	StateCrashed State = "crashed"
	// StateExited is a command that finished without an error.
	StateExited  State = "exited"
	StateStopped State = "stopped"
)

// tailLines is how much of a command's output Pier keeps for pier status.
const tailLines = 20

// ProcessStatus is one command's state.
type ProcessStatus struct {
	Name  string `json:"name"`
	State State  `json:"state"`
	// Exit says how the command last ended, such as "exited with code 1".
	Exit     string    `json:"exit,omitempty"`
	Restarts int       `json:"restarts,omitempty"`
	Since    time.Time `json:"since"`
	// Output is the end of what the command printed, once it is not running.
	Output []string `json:"output,omitempty"`
}

// Reason is how the command last ended, short: "exit 1", or the error.
func (p ProcessStatus) Reason() string {
	if code, ok := strings.CutPrefix(p.Exit, "exited with code "); ok {
		return "exit " + code
	}
	return p.Exit
}

// LastLine is the last thing the command printed, usually its error.
func (p ProcessStatus) LastLine() string {
	if len(p.Output) == 0 {
		return ""
	}
	return p.Output[len(p.Output)-1]
}

// Report is what a pier up that runs commands records for pier status.
type Report struct {
	PID       int             `json:"pid"`
	UpdatedAt time.Time       `json:"updatedAt"`
	Processes []ProcessStatus `json:"processes"`
}

// StateFile is where pier up records its commands for the project in dir.
func StateFile(dir string) string { return filepath.Join(dir, "run.json") }

// ReadReport reads what pier up recorded. Once that pier up has gone, only
// commands that ended on their own are kept: what it said was running is no
// longer true.
func ReadReport(path string) (Report, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Report{}, nil
	}
	if err != nil {
		return Report{}, err
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return Report{}, err
	}
	if report.PID != 0 && !alive(report.PID) {
		kept := report.Processes[:0]
		for _, process := range report.Processes {
			if process.State == StateCrashed || process.State == StateExited {
				kept = append(kept, process)
			}
		}
		report.Processes = kept
	}
	return report, nil
}

func writeReport(path string, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ansiEscape matches the color and cursor codes dev servers print.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// tail keeps the last non-blank lines a command printed, without colors.
type tail struct{ lines []string }

func (t *tail) add(line string) {
	line = strings.TrimRight(ansiEscape.ReplaceAllString(line, ""), "\r\n \t")
	if strings.TrimSpace(line) == "" {
		return
	}
	if len(t.lines) == tailLines {
		t.lines = append(t.lines[:0], t.lines[1:]...)
	}
	t.lines = append(t.lines, line)
}

func (t *tail) snapshot() []string {
	if len(t.lines) == 0 {
		return nil
	}
	return append([]string(nil), t.lines...)
}
