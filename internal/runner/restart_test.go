package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fastRestarts shrinks the backoff so crash loops finish in milliseconds.
func fastRestarts(t *testing.T, crashes int) {
	t.Helper()
	delay, ceiling, limit := restartDelay, maxRestartDelay, maxCrashes
	restartDelay, maxRestartDelay, maxCrashes = 10*time.Millisecond, 40*time.Millisecond, crashes
	t.Cleanup(func() { restartDelay, maxRestartDelay, maxCrashes = delay, ceiling, limit })
}

func TestBackoffDoublesUpToTheCeiling(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for i, delay := range want {
		if got := backoff(i + 1); got != delay {
			t.Errorf("backoff(%d) = %s, want %s", i+1, got, delay)
		}
	}
}

func TestSupervisorRestartsAFailureThenGivesUpKeepingItsOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	fastRestarts(t, 3)
	var out syncBuffer
	stateFile := filepath.Join(t.TempDir(), "run.json")
	command := `echo "starting"; printf '\033[31mError: listen EADDRINUSE :4000\033[0m\n'; exit 1`
	s := Start(context.Background(), []Process{{Name: "api", Command: command, Dir: t.TempDir()}}, &out, Options{StateFile: stateFile})
	s.Wait()

	text := out.String()
	if got := strings.Count(text, "api │ starting"); got != 3 {
		t.Fatalf("started %d times, want 3:\n%s", got, text)
	}
	for _, want := range []string{
		"api │ pier: exited with code 1; restarting in 10ms",
		"api │ pier: exited with code 1; restarting in 20ms",
		"api │ pier: exited with code 1; it crashed 3 times in 1m, so Pier stopped restarting it",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}

	report, err := ReadReport(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Processes) != 1 {
		t.Fatalf("report = %+v", report)
	}
	api := report.Processes[0]
	if api.State != StateCrashed || api.Exit != "exited with code 1" || api.Restarts != 2 {
		t.Errorf("api = %+v, want crashed after 2 restarts", api)
	}
	if api.LastLine() != "Error: listen EADDRINUSE :4000" {
		t.Errorf("last line = %q, want the error without colors", api.LastLine())
	}
}

func TestSupervisorDoesNotRestartACleanExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	fastRestarts(t, 3)
	var out syncBuffer
	s := Start(context.Background(), []Process{{Name: "job", Command: "echo done", Dir: t.TempDir()}}, &out, Options{})
	s.Wait()
	if strings.Contains(out.String(), "restarting") {
		t.Fatalf("a clean exit restarted:\n%s", out.String())
	}
	if got := s.Status(); len(got) != 1 || got[0].State != StateExited || got[0].LastLine() != "done" {
		t.Fatalf("status = %+v, want exited with its output", got)
	}
}

func TestSupervisorStopsDuringBackoff(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	delay, ceiling := restartDelay, maxRestartDelay
	restartDelay, maxRestartDelay = time.Hour, time.Hour
	t.Cleanup(func() { restartDelay, maxRestartDelay = delay, ceiling })
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	s := Start(ctx, []Process{{Name: "api", Command: "exit 1", Dir: t.TempDir()}}, &out, Options{})
	waitFor(t, &out, "restarting in 1h0m0s")
	if got := s.Active(); len(got) != 1 {
		t.Fatalf("active = %v, want the restarting process", got)
	}
	cancel()
	s.Wait()
	if got := s.Status(); got[0].State != StateStopped {
		t.Fatalf("status = %+v, want stopped", got)
	}
}

func TestReadReportForgetsRunningCommandsOfAGonePierUp(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "run.json")
	report := Report{PID: cmd.Process.Pid, Processes: []ProcessStatus{
		{Name: "web", State: StateRunning},
		{Name: "api", State: StateCrashed, Exit: "exited with code 1"},
		{Name: "worker", State: StateRestarting},
	}}
	if err := writeReport(path, report); err != nil {
		t.Fatal(err)
	}
	got, err := ReadReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Processes) != 1 || got.Processes[0].Name != "api" {
		t.Fatalf("processes = %+v, want only the crashed one", got.Processes)
	}

	report.PID = os.Getpid()
	if err := writeReport(path, report); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadReport(path); len(got.Processes) != 3 {
		t.Fatalf("processes = %+v, want all three while pier up runs", got.Processes)
	}
}

func TestTailKeepsTheLastLines(t *testing.T) {
	var lines tail
	for i := range tailLines + 5 {
		lines.add(fmt.Sprintf("line %d\n", i))
		lines.add("   \n")
	}
	got := lines.snapshot()
	if len(got) != tailLines || got[0] != "line 5" || got[len(got)-1] != fmt.Sprintf("line %d", tailLines+4) {
		t.Fatalf("tail = %q", got)
	}
}
