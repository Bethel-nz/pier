package cli

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/localname"
	"github.com/Bethel-nz/pier/internal/state"
	"github.com/Bethel-nz/pier/internal/tailscale"
	"github.com/Bethel-nz/pier/internal/tui"
)

// App is the CLI's view of the shared application service.
type App interface {
	Validate(ctx context.Context, req app.ValidateRequest) (app.ValidateResult, error)
	Plan(ctx context.Context, req app.PlanRequest) (app.PlanResult, error)
	Up(ctx context.Context, req app.UpRequest) (app.UpResult, error)
	Down(ctx context.Context, req app.DownRequest) (app.DownResult, error)
	Status(ctx context.Context, req app.StatusRequest) (app.StatusResult, error)
	Doctor(ctx context.Context, req app.DoctorRequest) (app.DoctorResult, error)
	Share(ctx context.Context, req app.ShareRequest) (app.ShareResult, error)
	Unshare(ctx context.Context, req app.UnshareRequest) (app.UnshareResult, error)
	Pause(ctx context.Context, req app.PauseRequest) (app.PauseResult, error)
	Resume(ctx context.Context, req app.ResumeRequest) (app.ResumeResult, error)
	AddService(ctx context.Context, req app.AddServiceRequest) (app.AddServiceResult, error)
	Open(ctx context.Context, req app.OpenRequest) (app.OpenResult, error)
	Copy(ctx context.Context, req app.CopyRequest) (app.CopyResult, error)
	Machine(ctx context.Context) (app.MachineResult, error)
}

type runtime struct {
	app     App
	local   *localname.Directory
	stdout  io.Writer
	stderr  io.Writer
	config  string
	json    bool
	verbose bool
	noColor bool
}

func (rt *runtime) start() string {
	if rt.config != "" {
		return rt.config
	}
	return "."
}

// Execute runs the pier command with the production application service.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return ExecuteWith(ctx, args, stdout, stderr, nil)
}

// ExecuteWith runs the pier command with an injected application service.
func ExecuteWith(ctx context.Context, args []string, stdout, stderr io.Writer, application App) error {
	var local *localname.Directory
	if application == nil {
		store, err := state.Open()
		if err != nil {
			return err
		}
		service := app.New(store, tailscale.ExecRunner{})
		local = localname.NewDirectory(store)
		service.EnableLocalNames(local)
		application = service
	}
	cmd := newRootCommand(stdout, stderr, application, local)
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return cmd.ExecuteContext(ctx)
}

func newRootCommand(stdout, stderr io.Writer, application App, local *localname.Directory) *cobra.Command {
	rt := &runtime{app: application, local: local, stdout: stdout, stderr: stderr}
	cmd := &cobra.Command{
		Use:           "pier",
		Short:         "Describe local services and get stable Tailscale URLs",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().StringVar(&rt.config, "config", "", "path to pier.yaml")
	cmd.PersistentFlags().BoolVar(&rt.json, "json", false, "machine-readable JSON output")
	cmd.PersistentFlags().BoolVar(&rt.verbose, "verbose", false, "include raw Tailscale stderr")
	cmd.PersistentFlags().BoolVar(&rt.noColor, "no-color", false, "disable color output")
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if tui.StdioIsTTY() {
			return runTUI(cmd.Context(), rt)
		}
		return cmd.Help()
	}
	cmd.AddCommand(
		newAgentsCommand(rt),
		newInitCommand(rt),
		newValidateCommand(rt),
		newPlanCommand(rt),
		newUpCommand(rt),
		newDownCommand(rt),
		newStatusCommand(rt),
		newDoctorCommand(rt),
		newShareCommand(rt),
		newUnshareCommand(rt),
		newPauseCommand(rt),
		newResumeCommand(rt),
		newServiceCommand(rt),
		newOpenCommand(rt),
		newCopyCommand(rt),
		newTUICommand(rt),
		newTrustCommand(rt),
		newQRCommand(rt),
		newLocaldCommand(),
	)
	if local != nil {
		cmd.PersistentPreRun = func(*cobra.Command, []string) {
			// pier up may ask the OS to trust Pier's CA, but only with a person at the terminal.
			local.Interactive = tui.StdioIsTTY() && !rt.json
		}
	}
	return cmd
}
