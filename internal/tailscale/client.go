// Package tailscale provides Pier's process boundary for the Tailscale CLI.
package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

const (
	funnelCapability = "funnel"
	httpsCapability  = "https"
)

// CLIDocs is Tailscale's page on finding and turning on its command-line
// tool, which is what Pier runs.
const CLIDocs = "https://tailscale.com/kb/1080/cli"

// cliHint says how to get a working tailscale command on each platform.
const cliHint = "Turn on Tailscale's CLI: " + CLIDocs +
	". On macOS: Tailscale → Settings → CLI integration → Install Now; with the App Store app, link it: " +
	"sudo ln -sf /Applications/Tailscale.app/Contents/MacOS/Tailscale /usr/local/bin/tailscale"

// Capabilities describes the Tailscale prerequisites Pier can use.
type Capabilities struct {
	Installed     bool
	DaemonRunning bool
	Authenticated bool
	MagicDNS      bool
	HTTPS         bool
	Funnel        bool
	// CLIWarning is set when the tailscale command does not match the
	// running Tailscale, such as an old Homebrew CLI beside the app.
	CLIWarning string
}

// Status holds node/session fields from `tailscale status --json` and, when
// filled by ParseStatus, Serve/Funnel routes. Client.Status does not populate Routes.
type Status struct {
	DNSName      string
	Routes       []Route
	BackendState string
	HaveNodeKey  bool
	MagicDNS     bool
	HTTPS        bool
	Funnel       bool
	// CLIWarning is set when the CLI and the daemon report different versions.
	CLIWarning string
}

// ErrorKind identifies a stable class of Tailscale diagnostic failure.
type ErrorKind string

const (
	ErrorCanceled           ErrorKind = "canceled"
	ErrorMissingExecutable  ErrorKind = "missing_executable"
	ErrorCommandFailed      ErrorKind = "command_failed"
	ErrorDaemonUnavailable  ErrorKind = "daemon_unavailable"
	ErrorNotAuthenticated   ErrorKind = "not_authenticated"
	ErrorInvalidStatus      ErrorKind = "invalid_status"
	ErrorFunnelUnauthorized ErrorKind = "funnel_unauthorized"
)

// CommandError carries a Pier-facing summary and details reserved for verbose output.
type CommandError struct {
	Kind    ErrorKind
	summary string
	stderr  string
	cause   error
}

func (e *CommandError) Error() string {
	return e.summary
}

// Unwrap exposes the underlying process or decoding failure.
func (e *CommandError) Unwrap() error {
	return e.cause
}

// VerboseDetails returns raw command stderr for an explicitly verbose renderer.
func (e *CommandError) VerboseDetails() string {
	return e.stderr
}

// Client reads Tailscale state through a Runner.
type Client struct {
	runner Runner
}

// NewClient creates a client. A nil runner selects the real process runner.
func NewClient(runner Runner) *Client {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Client{runner: runner}
}

// Check performs read-only Tailscale prerequisite checks.
func (c *Client) Check(ctx context.Context) (Capabilities, error) {
	var capabilities Capabilities

	name, args := versionCommand()
	_, stderr, err := c.runner.Run(ctx, name, args...)
	if err != nil && daemonUnavailableDiagnostic(string(stderr)) {
		return Capabilities{Installed: true}, daemonUnavailable(string(stderr), err)
	}
	if err != nil {
		commandErr := classifyRunError(ctx, err, stderr, "The tailscale command on your PATH does not work. "+cliHint)
		if commandErr.Kind != ErrorMissingExecutable {
			capabilities.Installed = true
		}
		return capabilities, commandErr
	}
	capabilities.Installed = true

	status, err := c.Status(ctx)
	if err != nil {
		var commandErr *CommandError
		if errors.As(err, &commandErr) && commandErr.Kind == ErrorCommandFailed && daemonUnavailableDiagnostic(commandErr.stderr) {
			return capabilities, daemonUnavailable(commandErr.stderr, commandErr.cause)
		}
		return capabilities, err
	}
	capabilities.CLIWarning = status.CLIWarning

	capabilities.DaemonRunning = status.BackendState != "Stopped"
	if !capabilities.DaemonRunning {
		return capabilities, &CommandError{
			Kind:    ErrorDaemonUnavailable,
			summary: "Tailscale is installed but turned off. Turn it on in the Tailscale app, or run tailscale up",
		}
	}

	capabilities.Authenticated = status.BackendState == "Running" && status.HaveNodeKey
	if !capabilities.Authenticated {
		return capabilities, &CommandError{
			Kind:    ErrorNotAuthenticated,
			summary: "Tailscale is running, but this device is not authenticated",
		}
	}
	capabilities.MagicDNS = status.MagicDNS
	capabilities.HTTPS = status.HTTPS
	capabilities.Funnel = status.Funnel

	name, args = funnelStatusCommand()
	_, stderr, err = c.runner.Run(ctx, name, args...)
	if err != nil {
		commandErr := classifyRunError(ctx, err, stderr, "Unable to read Tailscale Funnel status")
		if commandErr.Kind != ErrorCommandFailed || !funnelUnauthorizedDiagnostic(commandErr.stderr) {
			return capabilities, commandErr
		}
		capabilities.Funnel = false
		return capabilities, &CommandError{
			Kind:    ErrorFunnelUnauthorized,
			summary: "Tailscale Funnel is not authorized for this device",
			stderr:  commandErr.stderr,
			cause:   commandErr.cause,
		}
	}
	if !capabilities.Funnel {
		return capabilities, &CommandError{
			Kind:    ErrorFunnelUnauthorized,
			summary: "Tailscale Funnel is not authorized for this device",
			stderr:  string(stderr),
			cause:   err,
		}
	}

	return capabilities, nil
}

// daemonUnavailable is a tailscale command that works but cannot reach
// Tailscale itself: the app is closed, or the command belongs to another
// install than the one running.
func daemonUnavailable(stderr string, cause error) *CommandError {
	return &CommandError{
		Kind: ErrorDaemonUnavailable,
		summary: "Tailscale is installed, but its daemon is not running. Open the Tailscale app (or start tailscaled). " +
			"If it is running, the tailscale command on your PATH may belong to another install. " + cliHint,
		stderr: stderr,
		cause:  cause,
	}
}

func daemonUnavailableDiagnostic(stderr string) bool {
	diagnostic := strings.ToLower(stderr)
	if strings.Contains(diagnostic, "failed to connect to local tailscale") || strings.Contains(diagnostic, "is tailscale running?") {
		return true
	}
	return strings.Contains(diagnostic, "tailscaled") &&
		(strings.Contains(diagnostic, "not running") || strings.Contains(diagnostic, "doesn't appear to be running"))
}

var versionMismatch = regexp.MustCompile(`client version "([^"]+)" != tailscaled server version "([^"]+)"`)

// cliWarning explains the CLI's own warning that it does not match the
// running Tailscale; Pier's commands may then fail in ways that are hard to read.
func cliWarning(stderr string) string {
	match := versionMismatch.FindStringSubmatch(stderr)
	if match == nil {
		return ""
	}
	client, server := match[1], match[2]
	// Show the release numbers alone unless they are equal; then only the
	// build suffix differs, and dropping it would hide the mismatch.
	if short, shortServer := strings.SplitN(client, "-", 2)[0], strings.SplitN(server, "-", 2)[0]; short != shortServer {
		client, server = short, shortServer
	}
	return fmt.Sprintf("the tailscale command on your PATH is version %s, but Tailscale runs %s. Use the CLI that came with Tailscale: %s",
		client, server, CLIDocs)
}

func funnelUnauthorizedDiagnostic(stderr string) bool {
	diagnostic := strings.ToLower(stderr)
	return strings.Contains(diagnostic, "funnel is not enabled") ||
		strings.Contains(diagnostic, "funnel authorization required") ||
		strings.Contains(diagnostic, "not authorized to use funnel")
}

// Status reads and decodes node/session status without reading route handlers.
func (c *Client) Status(ctx context.Context) (Status, error) {
	name, args := nodeStatusCommand()
	stdout, stderr, err := c.runner.Run(ctx, name, args...)
	if err != nil {
		return Status{}, classifyRunError(ctx, err, stderr, "Unable to read Tailscale status")
	}
	warning := cliWarning(string(stderr))

	var response nodeStatusResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return Status{}, &CommandError{
			Kind:    ErrorInvalidStatus,
			summary: "Tailscale returned invalid status data",
			cause:   err,
		}
	}

	status := Status{
		BackendState: response.BackendState,
		HaveNodeKey:  response.HaveNodeKey,
		MagicDNS:     response.CurrentTailnet.MagicDNSEnabled,
		CLIWarning:   warning,
	}
	if response.Self != nil {
		status.DNSName = response.Self.DNSName
		status.Funnel = hasCapability(response.Self.CapMap, response.Self.Capabilities, funnelCapability)
		status.HTTPS = hasCapability(response.Self.CapMap, response.Self.Capabilities, httpsCapability)
	}
	status.HTTPS = status.HTTPS || len(response.CertDomains) > 0
	return status, nil
}

type nodeStatusResponse struct {
	BackendState   string `json:"BackendState"`
	HaveNodeKey    bool   `json:"HaveNodeKey"`
	CertDomains    []string
	CurrentTailnet struct {
		MagicDNSEnabled bool `json:"MagicDNSEnabled"`
	} `json:"CurrentTailnet"`
	Self *struct {
		DNSName      string                     `json:"DNSName"`
		Capabilities []string                   `json:"Capabilities"`
		CapMap       map[string]json.RawMessage `json:"CapMap"`
	} `json:"Self"`
}

func hasCapability(capMap map[string]json.RawMessage, capabilities []string, wanted string) bool {
	if _, ok := capMap[wanted]; ok {
		return true
	}
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

func classifyRunError(ctx context.Context, err error, stderr []byte, summary string) *CommandError {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return &CommandError{
			Kind:    ErrorCanceled,
			summary: "Tailscale diagnostics were canceled",
			stderr:  string(stderr),
			cause:   ctxErr,
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &CommandError{
			Kind:    ErrorCanceled,
			summary: "Tailscale diagnostics were canceled",
			stderr:  string(stderr),
			cause:   err,
		}
	}
	var executableErr *exec.Error
	if errors.As(err, &executableErr) {
		return &CommandError{
			Kind: ErrorMissingExecutable,
			summary: "Pier cannot find the tailscale command. Install Tailscale from https://tailscale.com/download. " +
				"If it is installed, its CLI is not on your PATH. " + cliHint,
			stderr: string(stderr),
			cause:  err,
		}
	}
	return &CommandError{
		Kind:    ErrorCommandFailed,
		summary: summary,
		stderr:  string(stderr),
		cause:   err,
	}
}
