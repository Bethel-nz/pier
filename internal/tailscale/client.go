// Package tailscale provides Pier's process boundary for the Tailscale CLI.
package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
)

const (
	funnelCapability = "funnel"
	httpsCapability  = "https"
)

// Capabilities describes the Tailscale prerequisites Pier can use.
type Capabilities struct {
	Installed     bool
	DaemonRunning bool
	Authenticated bool
	MagicDNS      bool
	HTTPS         bool
	Funnel        bool
}

// Status contains the node and session state reported by tailscale status.
// Route state is deliberately decoded separately from Serve and Funnel status.
type Status struct {
	DNSName      string
	BackendState string
	HaveNodeKey  bool
	MagicDNS     bool
	HTTPS        bool
	Funnel       bool
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
	if err != nil {
		commandErr := classifyRunError(ctx, err, stderr, "Unable to read the Tailscale version")
		if commandErr.Kind != ErrorMissingExecutable {
			capabilities.Installed = true
		}
		return capabilities, commandErr
	}
	capabilities.Installed = true

	status, err := c.Status(ctx)
	if err != nil {
		var commandErr *CommandError
		if errors.As(err, &commandErr) && commandErr.Kind == ErrorCommandFailed {
			return capabilities, &CommandError{
				Kind:    ErrorDaemonUnavailable,
				summary: "Tailscale is installed, but its daemon is not running",
				stderr:  commandErr.stderr,
				cause:   commandErr.cause,
			}
		}
		return capabilities, err
	}

	capabilities.DaemonRunning = status.BackendState != "Stopped"
	if !capabilities.DaemonRunning {
		return capabilities, &CommandError{
			Kind:    ErrorDaemonUnavailable,
			summary: "Tailscale is installed, but its daemon is not running",
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
		if commandErr.Kind == ErrorCanceled || commandErr.Kind == ErrorMissingExecutable {
			return capabilities, commandErr
		}
		if capabilities.Funnel {
			return capabilities, commandErr
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

// Status reads and decodes node/session status without reading route handlers.
func (c *Client) Status(ctx context.Context) (Status, error) {
	name, args := nodeStatusCommand()
	stdout, stderr, err := c.runner.Run(ctx, name, args...)
	if err != nil {
		return Status{}, classifyRunError(ctx, err, stderr, "Unable to read Tailscale status")
	}

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
			Kind:    ErrorMissingExecutable,
			summary: "Tailscale is not installed or is not available on PATH",
			stderr:  string(stderr),
			cause:   err,
		}
	}
	return &CommandError{
		Kind:    ErrorCommandFailed,
		summary: summary,
		stderr:  string(stderr),
		cause:   err,
	}
}
