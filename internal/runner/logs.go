package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// maxLogBytes is how big one service's log grows before it is moved aside to
// <name>.log.1, replacing the older one. Two files per service bound the disk.
var maxLogBytes int64 = 2 << 20

// maxLineBytes bounds how much of a line without a newline Pier holds before
// it writes what it has, so a service printing one huge payload can't grow
// Pier's memory without end.
const maxLineBytes = 64 << 10

const logTimeFormat = "2006-01-02T15:04:05.000Z"

// LogDir is where pier up keeps the project's run: command logs, under the
// same directory as the command state.
func LogDir(dir string) string { return filepath.Join(dir, "logs") }

// logSink appends each service's lines to <dir>/<name>.log, one timestamped
// line each. The prefixer's lock serializes its calls.
type logSink struct {
	dir   string
	files map[string]*logFile
	err   error
}

type logFile struct {
	f    *os.File
	size int64
}

func newLogSink(dir string) *logSink {
	return &logSink{dir: dir, files: map[string]*logFile{}}
}

func (s *logSink) write(name, text string) {
	text = strings.TrimRight(text, "\r\n")
	line := time.Now().UTC().Format(logTimeFormat) + " " + text + "\n"
	lf, err := s.open(name)
	if err != nil {
		s.err = err
		return
	}
	if lf.size > 0 && lf.size+int64(len(line)) > maxLogBytes {
		_ = lf.f.Close()
		delete(s.files, name)
		path := filepath.Join(s.dir, name+".log")
		_ = os.Rename(path, path+".1")
		if lf, err = s.open(name); err != nil {
			s.err = err
			return
		}
	}
	n, err := lf.f.WriteString(line)
	lf.size += int64(n)
	if err != nil {
		s.err = err
	}
}

func (s *logSink) open(name string) (*logFile, error) {
	if lf, ok := s.files[name]; ok {
		return lf, nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, name+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	lf := &logFile{f: f, size: info.Size()}
	s.files[name] = lf
	return lf, nil
}

func (s *logSink) close() {
	for name, lf := range s.files {
		_ = lf.f.Close()
		delete(s.files, name)
	}
}

// Entry is one logged line.
type Entry struct {
	Time    string
	Service string
	Text    string
}

// ReadLogs returns the last n lines of the named services' logs, oldest
// first, or of every service in dir when names is empty.
func ReadLogs(dir string, names []string, n int) ([]Entry, error) {
	if len(names) == 0 {
		names = LogServices(dir)
	}
	var all []Entry
	for _, name := range names {
		for _, suffix := range []string{".log.1", ".log"} {
			entries, _, err := readFrom(filepath.Join(dir, name+suffix), name, 0)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			all = append(all, entries...)
		}
	}
	sortEntries(all)
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// LogServices lists the services that have a log in dir.
func LogServices(dir string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, strings.TrimSuffix(filepath.Base(file), ".log"))
	}
	sort.Strings(names)
	return names
}

// readFrom reads the complete lines of path after offset and returns the
// offset to continue from. A last line without its newline is left for later.
func readFrom(path, name string, offset int64) ([]Entry, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	reader := bufio.NewReader(f)
	var entries []Entry
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break // EOF; a partial line waits for its newline
		}
		offset += int64(len(line))
		line = strings.TrimSuffix(line, "\n")
		ts, text, _ := strings.Cut(line, " ")
		entries = append(entries, Entry{Time: ts, Service: name, Text: text})
	}
	return entries, offset, nil
}

func sortEntries(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Time < entries[j].Time })
}

// PrintLogs writes entries under their service's prefix, the way pier up does.
func PrintLogs(w io.Writer, entries []Entry, services []string, color bool) {
	p := newPrefixer(io.Discard, services, color)
	for _, e := range entries {
		if _, ok := p.colors[e.Service]; !ok {
			p.colors[e.Service] = palette[0]
		}
		_, _ = io.WriteString(w, p.prefix(e.Service)+e.Text+"\n")
	}
}

// FollowLogs prints the last n lines, then each new line as it is logged,
// until ctx ends. A rotated log is read again from its start.
func FollowLogs(ctx context.Context, w io.Writer, dir string, names []string, n int, color bool) error {
	services := func() []string {
		if len(names) > 0 {
			return names
		}
		return LogServices(dir)
	}
	known := services()
	first, err := ReadLogs(dir, names, n)
	if err != nil {
		return err
	}
	PrintLogs(w, first, known, color)
	offsets := map[string]int64{}
	for _, name := range known {
		if info, err := os.Stat(filepath.Join(dir, name+".log")); err == nil {
			offsets[name] = info.Size()
		}
	}
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		var batch []Entry
		for _, name := range services() {
			path := filepath.Join(dir, name+".log")
			if info, err := os.Stat(path); err == nil && info.Size() < offsets[name] {
				offsets[name] = 0
			}
			entries, next, err := readFrom(path, name, offsets[name])
			if err != nil {
				continue
			}
			offsets[name] = next
			batch = append(batch, entries...)
		}
		if len(batch) == 0 {
			continue
		}
		sortEntries(batch)
		for _, name := range services() {
			if !slices.Contains(known, name) {
				known = append(known, name)
			}
		}
		PrintLogs(w, batch, known, color)
	}
}

// Summary is the quiet-mode line under the routes.
func Summary(statuses []ProcessStatus) string {
	running, failed := 0, 0
	for _, status := range statuses {
		switch status.State {
		case StateRunning, StateRestarting:
			running++
		case StateCrashed:
			failed++
		}
	}
	text := fmt.Sprintf("%d running", running)
	if failed > 0 {
		text += fmt.Sprintf(" · %d failed", failed)
	}
	return text
}
