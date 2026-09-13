package tailscale

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

type call struct {
	name string
	args []string
}

type fakeResponse struct {
	out    []byte
	err    []byte
	runErr error
}

type fakeRunner struct {
	calls  []call
	out    []byte
	err    []byte
	runErr error
	queued []fakeResponse
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.calls = append(f.calls, call{name: name, args: append([]string(nil), args...)})
	if len(f.queued) == 0 {
		return f.out, f.err, f.runErr
	}
	response := f.queued[0]
	f.queued = f.queued[1:]
	return response.out, response.err, response.runErr
}

func TestClientCheck(t *testing.T) {
	connectedStatus := []byte(`{
		"BackendState": "Running",
		"HaveNodeKey": true,
		"Self": {
			"DNSName": "pier-test.example.ts.net.",
			"CapMap": {"funnel": null, "https": null}
		},
		"CurrentTailnet": {"MagicDNSEnabled": true},
		"CertDomains": []
	}`)

	t.Run("reports a missing executable without claiming installation", func(t *testing.T) {
		runner := &fakeRunner{runErr: &exec.Error{Name: "tailscale", Err: exec.ErrNotFound}}

		capabilities, err := NewClient(runner).Check(context.Background())

		if capabilities != (Capabilities{}) {
			t.Errorf("Check() capabilities = %#v, want no available capabilities", capabilities)
		}
		assertCommandError(t, err, ErrorMissingExecutable, "Tailscale is not installed or is not available on PATH", "")
		assertCalls(t, runner.calls, call{name: "tailscale", args: []string{"version"}})
	})

	t.Run("distinguishes an unavailable daemon from a missing executable", func(t *testing.T) {
		runner := &fakeRunner{queued: []fakeResponse{
			{out: []byte("1.88.2\n")},
			{err: []byte("failed to connect to local tailscaled; it doesn't appear to be running\n"), runErr: errors.New("exit status 1")},
		}}

		capabilities, err := NewClient(runner).Check(context.Background())

		if capabilities != (Capabilities{Installed: true}) {
			t.Errorf("Check() capabilities = %#v, want installed-only result", capabilities)
		}
		assertCommandError(t, err, ErrorDaemonUnavailable, "Tailscale is installed, but its daemon is not running", "failed to connect to local tailscaled; it doesn't appear to be running\n")
		assertCalls(t, runner.calls,
			call{name: "tailscale", args: []string{"version"}},
			call{name: "tailscale", args: []string{"status", "--json"}},
		)
	})

	t.Run("distinguishes a stopped backend from a missing executable", func(t *testing.T) {
		runner := &fakeRunner{queued: []fakeResponse{
			{out: []byte("1.88.2\n")},
			{out: []byte(`{"BackendState":"Stopped","HaveNodeKey":true}`)},
		}}

		capabilities, err := NewClient(runner).Check(context.Background())

		if capabilities != (Capabilities{Installed: true}) {
			t.Errorf("Check() capabilities = %#v, want installed-only result", capabilities)
		}
		assertCommandError(t, err, ErrorDaemonUnavailable, "Tailscale is installed, but its daemon is not running", "")
	})

	t.Run("reports a running daemon that needs authentication", func(t *testing.T) {
		runner := &fakeRunner{queued: []fakeResponse{
			{out: []byte("1.88.2\n")},
			{out: []byte(`{"BackendState":"NeedsLogin","HaveNodeKey":false}`)},
		}}

		capabilities, err := NewClient(runner).Check(context.Background())

		want := Capabilities{Installed: true, DaemonRunning: true}
		if capabilities != want {
			t.Errorf("Check() capabilities = %#v, want %#v", capabilities, want)
		}
		assertCommandError(t, err, ErrorNotAuthenticated, "Tailscale is running, but this device is not authenticated", "")
		assertCalls(t, runner.calls,
			call{name: "tailscale", args: []string{"version"}},
			call{name: "tailscale", args: []string{"status", "--json"}},
		)
	})

	t.Run("returns connected capabilities from node status", func(t *testing.T) {
		runner := &fakeRunner{queued: []fakeResponse{
			{out: []byte("1.88.2\n")},
			{out: connectedStatus},
			{out: []byte("{}\n")},
		}}

		capabilities, err := NewClient(runner).Check(context.Background())

		if err != nil {
			t.Fatalf("Check() error = %v", err)
		}
		want := Capabilities{
			Installed:     true,
			DaemonRunning: true,
			Authenticated: true,
			MagicDNS:      true,
			HTTPS:         true,
			Funnel:        true,
		}
		if capabilities != want {
			t.Errorf("Check() capabilities = %#v, want %#v", capabilities, want)
		}
		assertCalls(t, runner.calls,
			call{name: "tailscale", args: []string{"version"}},
			call{name: "tailscale", args: []string{"status", "--json"}},
			call{name: "tailscale", args: []string{"funnel", "status", "--json"}},
		)
	})

	t.Run("reports when Funnel authorization is required", func(t *testing.T) {
		statusWithoutFunnel := []byte(`{
			"BackendState": "Running",
			"HaveNodeKey": true,
			"Self": {"DNSName": "pier-test.example.ts.net.", "CapMap": {"https": null}},
			"CurrentTailnet": {"MagicDNSEnabled": true},
			"CertDomains": ["pier-test.example.ts.net"]
		}`)
		runner := &fakeRunner{queued: []fakeResponse{
			{out: []byte("1.88.2\n")},
			{out: statusWithoutFunnel},
			{err: []byte("Funnel is not enabled on this tailnet\n"), runErr: errors.New("exit status 1")},
		}}

		capabilities, err := NewClient(runner).Check(context.Background())

		want := Capabilities{Installed: true, DaemonRunning: true, Authenticated: true, MagicDNS: true, HTTPS: true}
		if capabilities != want {
			t.Errorf("Check() capabilities = %#v, want %#v", capabilities, want)
		}
		assertCommandError(t, err, ErrorFunnelUnauthorized, "Tailscale Funnel is not authorized for this device", "Funnel is not enabled on this tailnet\n")
		assertCalls(t, runner.calls,
			call{name: "tailscale", args: []string{"version"}},
			call{name: "tailscale", args: []string{"status", "--json"}},
			call{name: "tailscale", args: []string{"funnel", "status", "--json"}},
		)
	})

	t.Run("preserves raw stderr for verbose rendering without exposing it by default", func(t *testing.T) {
		const rawStderr = "backend secret diagnostic: socket=/private/path\n"
		runner := &fakeRunner{queued: []fakeResponse{
			{out: []byte("1.88.2\n")},
			{err: []byte(rawStderr), runErr: errors.New("exit status 1")},
		}}

		_, err := NewClient(runner).Check(context.Background())

		commandErr := assertCommandError(t, err, ErrorDaemonUnavailable, "Tailscale is installed, but its daemon is not running", rawStderr)
		if strings.Contains(commandErr.Error(), rawStderr) || strings.Contains(commandErr.Error(), "socket=/private/path") {
			t.Errorf("default error %q exposes raw stderr", commandErr.Error())
		}
	})

	t.Run("returns a typed invalid status error for malformed JSON", func(t *testing.T) {
		runner := &fakeRunner{queued: []fakeResponse{
			{out: []byte("1.88.2\n")},
			{out: []byte(`{"BackendState":`)},
		}}

		capabilities, err := NewClient(runner).Check(context.Background())

		if capabilities != (Capabilities{Installed: true}) {
			t.Errorf("Check() capabilities = %#v, want installed-only result", capabilities)
		}
		assertCommandError(t, err, ErrorInvalidStatus, "Tailscale returned invalid status data", "")
	})

	t.Run("returns a typed cancellation error", func(t *testing.T) {
		runner := &fakeRunner{runErr: context.Canceled}

		_, err := NewClient(runner).Check(context.Background())

		assertCommandError(t, err, ErrorCanceled, "Tailscale diagnostics were canceled", "")
	})

	t.Run("keeps cancellation precedence during the Funnel probe", func(t *testing.T) {
		statusWithoutFunnel := []byte(`{
			"BackendState": "Running",
			"HaveNodeKey": true,
			"Self": {"CapMap": {"https": null}},
			"CurrentTailnet": {"MagicDNSEnabled": true}
		}`)
		runner := &fakeRunner{queued: []fakeResponse{
			{out: []byte("1.88.2\n")},
			{out: statusWithoutFunnel},
			{err: []byte("probe canceled\n"), runErr: context.Canceled},
		}}

		_, err := NewClient(runner).Check(context.Background())

		assertCommandError(t, err, ErrorCanceled, "Tailscale diagnostics were canceled", "probe canceled\n")
	})
}

func TestExecRunnerCapturesStdoutAndStderrSeparately(t *testing.T) {
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == "runner-helper" {
		_, _ = os.Stdout.WriteString("status output")
		_, _ = os.Stderr.WriteString("diagnostic output")
		return
	}

	stdout, stderr, err := (ExecRunner{}).Run(
		context.Background(),
		os.Args[0],
		"-test.run=TestExecRunnerCapturesStdoutAndStderrSeparately",
		"--",
		"runner-helper",
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(string(stdout), "status output") {
		t.Errorf("Run() stdout = %q, want helper output", stdout)
	}
	if strings.Contains(string(stdout), "diagnostic output") {
		t.Errorf("Run() stdout = %q, want no stderr content", stdout)
	}
	if !strings.Contains(string(stderr), "diagnostic output") {
		t.Errorf("Run() stderr = %q, want helper diagnostic", stderr)
	}
	if strings.Contains(string(stderr), "status output") {
		t.Errorf("Run() stderr = %q, want no stdout content", stderr)
	}
}

func assertCommandError(t *testing.T, err error, kind ErrorKind, summary, stderr string) *CommandError {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want a typed command error")
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type = %T, want *CommandError", err)
	}
	if commandErr.Kind != kind {
		t.Errorf("error kind = %q, want %q", commandErr.Kind, kind)
	}
	if commandErr.Error() != summary {
		t.Errorf("error summary = %q, want %q", commandErr.Error(), summary)
	}
	if commandErr.VerboseDetails() != stderr {
		t.Errorf("verbose details = %q, want %q", commandErr.VerboseDetails(), stderr)
	}
	return commandErr
}

func assertCalls(t *testing.T, got []call, want ...call) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("runner calls = %#v, want %#v", got, want)
	}
}
