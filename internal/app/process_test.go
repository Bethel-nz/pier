package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bethel-nz/pier/internal/runner"
)

func TestStatusShowsWhatPierUpRecordedAboutItsCommands(t *testing.T) {
	env := newEnv()
	env.actual = env.desiredRoutes()
	env.state.Routes = ownAll(env.actual)
	report := runner.Report{PID: os.Getpid(), Processes: []runner.ProcessStatus{
		{Name: "api", State: runner.StateCrashed, Exit: "exited with code 1", Output: []string{"panic: boom"}},
	}}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(env.project.Root, ".pier")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runner.StateFile(dir), data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := env.service().Status(context.Background(), StatusRequest{Start: env.project.Root})
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range result.Services {
		switch {
		case info.Name == "api" && (info.Process == nil || info.Process.LastLine() != "panic: boom"):
			t.Errorf("api process = %+v, want the crash", info.Process)
		case info.Name != "api" && info.Process != nil:
			t.Errorf("%s process = %+v, want none", info.Name, info.Process)
		}
	}
}
