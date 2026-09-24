package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/runner"
)

// readyTimeout bounds how long pier up waits for run: commands to listen
// before it applies routes anyway.
var readyTimeout = 60 * time.Second

// up applies routes. With run: commands it starts them first and stays in the
// foreground with their output until Ctrl-C, like docker compose up.
func (rt *runtime) up(ctx context.Context, req app.UpRequest) error {
	plan, err := rt.app.RunPlan(req.Start)
	if err != nil || len(plan.Processes) == 0 {
		result, upErr := rt.app.Up(ctx, req)
		return rt.renderer("up").Up(result, upErr)
	}
	lock, owned, err := runner.Acquire(filepath.Join(plan.Project.Root, ".pier"))
	if err != nil {
		return rt.renderer("up").Error(fmt.Errorf("Pier could not lock the project to run it: %w", err))
	}
	if !owned {
		fmt.Fprintln(rt.stderr, "another pier up is already running this project's commands; updating routes only")
		result, upErr := rt.app.Up(ctx, req)
		return rt.renderer("up").Up(result, upErr)
	}
	defer lock.Release()

	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	logs := rt.stdout
	if rt.json {
		logs = rt.stderr // keep stdout valid JSON
	}
	supervisor := runner.Start(runCtx, plan.Processes, logs, rt.colorFor(logs))
	waitForTargets(runCtx, supervisor, plan.Targets)
	if runCtx.Err() != nil {
		supervisor.Wait()
		return nil
	}

	result, upErr := rt.app.Up(ctx, req)
	if renderErr := rt.renderer("up").Up(result, upErr); renderErr != nil {
		stop()
		supervisor.Wait()
		return renderErr
	}
	fmt.Fprintln(rt.stderr, "Ctrl-C stops the commands; routes stay until pier down")
	supervisor.Wait()
	return nil
}

// waitForTargets returns once every target accepts connections, every
// command has exited, ctx ends, or readyTimeout passes.
func waitForTargets(ctx context.Context, supervisor *runner.Supervisor, targets map[string]string) {
	pending := make(map[string]string, len(targets))
	for name, address := range targets {
		pending[name] = address
	}
	deadline := time.Now().Add(readyTimeout)
	started := time.Now()
	announced := false
	for len(pending) > 0 && time.Now().Before(deadline) && ctx.Err() == nil {
		for name, address := range pending {
			if conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond); err == nil {
				_ = conn.Close()
				delete(pending, name)
			}
		}
		if len(pending) == 0 {
			return
		}
		if len(supervisor.Running()) == 0 && time.Since(started) > time.Second {
			return // nothing left that could start listening
		}
		if !announced && time.Since(started) > 2*time.Second {
			announced = true
			names := make([]string, 0, len(pending))
			for name := range pending {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				supervisor.Notef(name, "waiting for %s to accept connections", pending[name])
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// colorFor colors prefixes only on a terminal, and never with --no-color or NO_COLOR.
func (rt *runtime) colorFor(w io.Writer) bool {
	if rt.noColor || os.Getenv("NO_COLOR") != "" {
		return false
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
