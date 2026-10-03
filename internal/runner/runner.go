// Package runner starts the commands a project declares with run:, streams
// their output, restarts them when they fail or watched files change, and
// stops them.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Process is one service Pier runs.
type Process struct {
	Name    string
	Command string
	Dir     string            // absolute working directory
	Env     map[string]string // added to Pier's environment
	Watch   []string          // globs, relative to Dir, that restart it
	Restart Restart
}

// Restart says whether a command starts again after it fails.
type Restart string

const (
	// RestartOnFailure, the default, restarts a command that exits with an
	// error, with backoff, until it crashes too often.
	RestartOnFailure Restart = "on-failure"
	RestartNever     Restart = "never"
)

// Options control how processes are run and reported.
type Options struct {
	// Color colors each process's prefix.
	Color bool
	// StateFile, when set, is where the processes' states are recorded for
	// pier status.
	StateFile string
}

// stopGrace is how long a process gets to exit after a polite stop.
var stopGrace = 5 * time.Second

// pollEvery is how often watched files are checked.
var pollEvery = 500 * time.Millisecond

// Restart backoff: the first restart waits restartDelay, each one after
// doubles it up to maxRestartDelay, and maxCrashes within crashWindow stop
// the restarts so a crash loop doesn't bury its own error.
var (
	restartDelay    = time.Second
	maxRestartDelay = 30 * time.Second
	crashWindow     = time.Minute
	maxCrashes      = 5
)

// Supervisor runs processes until its context ends.
type Supervisor struct {
	out       *prefixer
	stateFile string
	wg        sync.WaitGroup
	mu        sync.Mutex
	live      map[string]bool
	states    map[string]ProcessStatus
	order     []string
}

// Start launches every process and returns at once. Output goes to out, one
// prefixed line at a time. Processes stop when ctx ends.
func Start(ctx context.Context, procs []Process, out io.Writer, opts Options) *Supervisor {
	names := make([]string, len(procs))
	for i, proc := range procs {
		names[i] = proc.Name
	}
	s := &Supervisor{
		out:       newPrefixer(out, names, opts.Color),
		stateFile: opts.StateFile,
		live:      map[string]bool{},
		states:    map[string]ProcessStatus{},
		order:     names,
	}
	for _, proc := range procs {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.supervise(ctx, proc)
		}()
	}
	return s
}

// Wait blocks until every process has stopped for good.
func (s *Supervisor) Wait() { s.wg.Wait() }

// Running lists the processes that are up right now.
func (s *Supervisor) Running() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.live))
	for name, up := range s.live {
		if up {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Active lists the processes that are up or about to restart: the ones that
// may still start listening.
func (s *Supervisor) Active() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var names []string
	for _, name := range s.order {
		if state := s.states[name].State; state == StateRunning || state == StateRestarting || state == "" {
			names = append(names, name)
		}
	}
	return names
}

// Status is each process's state, in the order they were given.
func (s *Supervisor) Status() []ProcessStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *Supervisor) statusLocked() []ProcessStatus {
	out := make([]ProcessStatus, 0, len(s.order))
	for _, name := range s.order {
		if status, ok := s.states[name]; ok {
			out = append(out, status)
		}
	}
	return out
}

func (s *Supervisor) setLive(name string, up bool) {
	s.mu.Lock()
	s.live[name] = up
	s.mu.Unlock()
}

// record sets a process's state and writes every state to the state file.
// Output is kept once the process is not running.
func (s *Supervisor) record(name string, state State, exit string, restarts int) {
	status := ProcessStatus{Name: name, State: state, Exit: exit, Restarts: restarts, Since: time.Now()}
	if state != StateRunning && state != StateStopped {
		status.Output = s.out.output(name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[name] = status
	if s.stateFile == "" {
		return
	}
	report := Report{PID: os.Getpid(), UpdatedAt: time.Now(), Processes: s.statusLocked()}
	if err := writeReport(s.stateFile, report); err != nil {
		s.out.note(name, "could not record its state for pier status: "+err.Error())
	}
}

// Notef prints a Pier line under a process's prefix.
func (s *Supervisor) Notef(name, format string, args ...any) {
	s.out.note(name, fmt.Sprintf(format, args...))
}

// outcome is how one run of a process ended.
type outcome struct {
	stopped bool   // ctx ended
	changed string // a watched file changed
	err     error  // the process exited, or could not start, with this error
}

// supervise runs one process. A failure restarts it with backoff until it
// crashes maxCrashes times within crashWindow; a clean exit, or a failure
// with restart: never, waits for a watched change. Without watch: it stays down.
func (s *Supervisor) supervise(ctx context.Context, proc Process) {
	var watch *watcher
	if len(proc.Watch) > 0 {
		watch = newWatcher(proc.Dir, proc.Watch)
	}
	var crashes []time.Time
	restarts := 0
	for {
		s.out.restart(proc.Name)
		var result outcome
		cmd, exited, err := s.start(proc)
		if err != nil {
			result.err = fmt.Errorf("could not start: %w", err)
		} else {
			s.record(proc.Name, StateRunning, "", restarts)
			result = s.hold(ctx, proc, cmd, exited, watch)
		}
		if result.stopped {
			s.record(proc.Name, StateStopped, "", restarts)
			return
		}
		if result.changed != "" {
			crashes, restarts = nil, 0
			s.Notef(proc.Name, "restarting: %s changed", result.changed)
			continue
		}

		exit := exitText(result.err)
		if result.err == nil || proc.Restart == RestartNever {
			s.Notef(proc.Name, "%s", exitMessage(exit, watch != nil))
			state := StateExited
			if result.err != nil {
				state = StateCrashed
			}
			s.record(proc.Name, state, exit, restarts)
			if !s.waitForChange(ctx, proc, watch) {
				return
			}
			crashes, restarts = nil, 0
			continue
		}

		now := time.Now()
		crashes = append(recent(crashes, now), now)
		if len(crashes) >= maxCrashes {
			message := fmt.Sprintf("%s; it crashed %d times in %s, so Pier stopped restarting it", exit, len(crashes), shortDuration(crashWindow))
			s.Notef(proc.Name, "%s", exitMessage(message, watch != nil))
			s.record(proc.Name, StateCrashed, exit, restarts)
			if !s.waitForChange(ctx, proc, watch) {
				return
			}
			crashes, restarts = nil, 0
			continue
		}
		delay := backoff(len(crashes))
		s.Notef(proc.Name, "%s; restarting in %s", exit, delay)
		s.record(proc.Name, StateRestarting, exit, restarts)
		changed, stopped := s.pause(ctx, watch, delay)
		if stopped {
			s.record(proc.Name, StateStopped, "", restarts)
			return
		}
		if changed != "" {
			crashes, restarts = nil, 0
			s.Notef(proc.Name, "restarting: %s changed", changed)
			continue
		}
		restarts++
	}
}

// waitForChange holds a process that is down until a watched file changes.
// It returns false when nothing will start it again.
func (s *Supervisor) waitForChange(ctx context.Context, proc Process, watch *watcher) bool {
	if watch == nil {
		return false
	}
	changed, stopped := s.pause(ctx, watch, 0)
	if stopped {
		s.record(proc.Name, StateStopped, "", 0)
		return false
	}
	s.Notef(proc.Name, "restarting: %s changed", changed)
	return true
}

// recent drops crashes older than crashWindow.
func recent(crashes []time.Time, now time.Time) []time.Time {
	kept := crashes[:0]
	for _, at := range crashes {
		if now.Sub(at) < crashWindow {
			kept = append(kept, at)
		}
	}
	return kept
}

// shortDuration drops a duration's zero tail: 1m rather than 1m0s.
func shortDuration(d time.Duration) string {
	text := d.String()
	if strings.HasSuffix(text, "m0s") {
		text = strings.TrimSuffix(text, "0s")
	}
	if strings.HasSuffix(text, "h0m") {
		text = strings.TrimSuffix(text, "0m")
	}
	return text
}

// backoff is the wait before restarting after the nth recent crash.
func backoff(crashes int) time.Duration {
	delay := restartDelay
	for i := 1; i < crashes && delay < maxRestartDelay; i++ {
		delay *= 2
	}
	return min(delay, maxRestartDelay)
}

// hold waits for the process to exit, a watched file to change, or ctx to end.
func (s *Supervisor) hold(ctx context.Context, proc Process, cmd *exec.Cmd, exited <-chan error, watch *watcher) outcome {
	var changes <-chan time.Time
	if watch != nil {
		tick := time.NewTicker(pollEvery)
		defer tick.Stop()
		changes = tick.C
	}
	for {
		select {
		case <-ctx.Done():
			stop(cmd, exited)
			s.Notef(proc.Name, "stopped")
			s.setLive(proc.Name, false)
			return outcome{stopped: true}
		case err := <-exited:
			s.setLive(proc.Name, false)
			return outcome{err: err}
		case <-changes:
			changed := watch.changed()
			if changed == "" {
				continue
			}
			time.Sleep(200 * time.Millisecond) // let an editor finish saving
			watch.changed()
			stop(cmd, exited)
			s.setLive(proc.Name, false)
			return outcome{changed: changed}
		}
	}
}

// pause waits d, or with d of 0 until a watched file changes. It returns the
// changed file, or stopped when ctx ends first.
func (s *Supervisor) pause(ctx context.Context, watch *watcher, d time.Duration) (changed string, stopped bool) {
	var timeout <-chan time.Time
	if d > 0 {
		timer := time.NewTimer(d)
		defer timer.Stop()
		timeout = timer.C
	}
	var changes <-chan time.Time
	if watch != nil {
		tick := time.NewTicker(pollEvery)
		defer tick.Stop()
		changes = tick.C
	}
	for {
		select {
		case <-ctx.Done():
			return "", true
		case <-timeout:
			return "", false
		case <-changes:
			if changed := watch.changed(); changed != "" {
				time.Sleep(200 * time.Millisecond) // let an editor finish saving
				watch.changed()
				return changed, false
			}
		}
	}
}

// exitText says how a process ended, such as "exited with code 1".
func exitText(err error) string {
	var exit *exec.ExitError
	switch {
	case err == nil:
		return "exited"
	case errors.As(err, &exit) && exit.ExitCode() >= 0:
		return "exited with code " + strconv.Itoa(exit.ExitCode())
	case errors.As(err, &exit):
		return "exited: " + exit.String()
	default:
		return err.Error()
	}
}

func exitMessage(message string, watching bool) string {
	if watching {
		message += "; waiting for a change to restart"
	}
	return message
}

func (s *Supervisor) start(proc Process) (*exec.Cmd, <-chan error, error) {
	cmd := shellCommand(proc.Command)
	cmd.Dir = proc.Dir
	cmd.Env = environ(proc.Env, s.out.color)
	cmd.Stdout = s.out.writer(proc.Name)
	cmd.Stderr = cmd.Stdout
	// A child that outlives the shell keeps the output pipe open; don't wait on it forever.
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	s.setLive(proc.Name, true)
	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		s.out.flush(proc.Name)
		exited <- err
	}()
	return cmd, exited, nil
}

// environ is Pier's environment plus env. FORCE_COLOR keeps dev servers
// colorful although their output is piped through Pier.
func environ(env map[string]string, color bool) []string {
	out := os.Environ()
	if color && os.Getenv("FORCE_COLOR") == "" && env["FORCE_COLOR"] == "" {
		out = append(out, "FORCE_COLOR=1")
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

// stop asks the process tree to exit, then kills it after stopGrace.
func stop(cmd *exec.Cmd, exited <-chan error) {
	terminate(cmd)
	select {
	case <-exited:
	case <-time.After(stopGrace):
		kill(cmd)
		<-exited
	}
}
