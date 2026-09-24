// Package runner starts the commands a project declares with run:, streams
// their output, restarts them when watched files change, and stops them.
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
}

// stopGrace is how long a process gets to exit after a polite stop.
var stopGrace = 5 * time.Second

// pollEvery is how often watched files are checked.
var pollEvery = 500 * time.Millisecond

// Supervisor runs processes until its context ends.
type Supervisor struct {
	out  *prefixer
	wg   sync.WaitGroup
	mu   sync.Mutex
	live map[string]bool
}

// Start launches every process and returns at once. Output goes to out, one
// prefixed line at a time. Processes stop when ctx ends.
func Start(ctx context.Context, procs []Process, out io.Writer, color bool) *Supervisor {
	names := make([]string, len(procs))
	for i, proc := range procs {
		names[i] = proc.Name
	}
	s := &Supervisor{out: newPrefixer(out, names, color), live: map[string]bool{}}
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

func (s *Supervisor) setLive(name string, up bool) {
	s.mu.Lock()
	s.live[name] = up
	s.mu.Unlock()
}

// Notef prints a Pier line under a process's prefix.
func (s *Supervisor) Notef(name, format string, args ...any) {
	s.out.note(name, fmt.Sprintf(format, args...))
}

// supervise runs one process: again after a watched change, never after a
// plain exit, since a crash loop would bury its own error.
func (s *Supervisor) supervise(ctx context.Context, proc Process) {
	var watch *watcher
	if len(proc.Watch) > 0 {
		watch = newWatcher(proc.Dir, proc.Watch)
	}
	for {
		cmd, exited, err := s.start(proc)
		if err != nil {
			s.Notef(proc.Name, "could not start: %v", err)
			if watch == nil {
				return
			}
		}
		reason := s.hold(ctx, proc, cmd, exited, watch)
		if reason == "" {
			return
		}
		s.Notef(proc.Name, "restarting: %s changed", reason)
	}
}

// hold waits for the process to exit, a watched file to change, or ctx to
// end. It returns the changed file when the process should start again.
func (s *Supervisor) hold(ctx context.Context, proc Process, cmd *exec.Cmd, exited <-chan error, watch *watcher) string {
	var changes <-chan time.Time
	if watch != nil {
		tick := time.NewTicker(pollEvery)
		defer tick.Stop()
		changes = tick.C
	}
	for {
		select {
		case <-ctx.Done():
			if cmd != nil && exited != nil {
				stop(cmd, exited)
				s.Notef(proc.Name, "stopped")
			}
			s.setLive(proc.Name, false)
			return ""
		case err := <-exited:
			exited = nil
			s.setLive(proc.Name, false)
			s.Notef(proc.Name, "%s", exitMessage(err, watch != nil))
			if watch == nil {
				return ""
			}
		case <-changes:
			changed := watch.changed()
			if changed == "" {
				continue
			}
			time.Sleep(200 * time.Millisecond) // let an editor finish saving
			watch.changed()
			if exited != nil {
				stop(cmd, exited)
			}
			return changed
		}
	}
}

func exitMessage(err error, watching bool) string {
	message := "exited"
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		message = "exited with code " + strconv.Itoa(exit.ExitCode())
	default:
		message = "exited: " + err.Error()
	}
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
