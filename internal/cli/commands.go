package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"pier/internal/app"
	"pier/internal/localname"
	"pier/internal/project"
	"pier/internal/render"
	"pier/internal/state"
	"pier/internal/tui"
)

func (rt *runtime) renderer(command string) render.Options {
	out, err := rt.stdout, rt.stderr
	if rt.json {
		err = rt.stdout
	}
	return render.Options{
		JSON:    rt.json,
		Verbose: rt.verbose,
		Command: command,
		Out:     out,
		Err:     err,
	}
}

func newLocaldCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "locald",
		Short:  "Publish local domains and move them when this machine's address changes",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := state.Open()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return localname.Run(ctx, store, &localname.DNSAnnouncer{})
		},
	}
}

func newInitCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "init [name]",
		Short: "Create a minimal pier.yaml and project identity",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			name := filepath.Base(dir)
			if len(args) == 1 {
				name = args[0]
			}
			ctx, err := project.Init(dir, name)
			if err != nil {
				return rt.renderer("init").Error(err)
			}
			fmt.Fprintf(rt.stdout, "initialized %s\n", ctx.ConfigPath)
			return nil
		},
	}
}

func newValidateCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate pier.yaml without requiring services to be running",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := rt.app.Validate(cmd.Context(), app.ValidateRequest{Start: rt.start()})
			return rt.renderer("validate").Validate(result, err)
		},
	}
}

func newPlanCommand(rt *runtime) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Print the non-mutating desired-versus-actual plan",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := rt.app.Plan(cmd.Context(), app.PlanRequest{Start: rt.start(), Force: force})
			return rt.renderer("plan").Plan(result, err)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "plan takeover of unmanaged routes")
	return cmd
}

func newUpCommand(rt *runtime) *cobra.Command {
	var force, strict bool
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Apply the project plan to Tailscale Serve and Funnel",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := rt.app.Up(cmd.Context(), app.UpRequest{Start: rt.start(), Force: force, Strict: strict})
			return rt.renderer("up").Up(result, err)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "take over unmanaged routes")
	cmd.Flags().BoolVar(&strict, "strict", false, "refuse to apply if a local target is unavailable")
	return cmd
}

func newDownCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Remove only routes owned by this Pier project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := rt.app.Down(cmd.Context(), app.DownRequest{Start: rt.start()})
			return rt.renderer("down").Down(result, err)
		},
	}
}

func newStatusCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show configured services, health, public access, and URLs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := rt.app.Status(cmd.Context(), app.StatusRequest{Start: rt.start()})
			return rt.renderer("status").Status(result, err)
		},
	}
}

func newDoctorCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose Pier config, Tailscale, and local targets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := rt.app.Doctor(cmd.Context(), app.DoctorRequest{Start: rt.start()})
			return rt.renderer("doctor").Doctor(result, err)
		},
	}
}

func newShareCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "share <service>",
		Short: "Apply a runtime-only public: true override",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rt.app.Share(cmd.Context(), app.ShareRequest{Start: rt.start(), Service: args[0]})
			return rt.renderer("share").Share(result, err)
		},
	}
}

func newUnshareCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "unshare <service>",
		Short: "Remove a runtime public override",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rt.app.Unshare(cmd.Context(), app.UnshareRequest{Start: rt.start(), Service: args[0]})
			return rt.renderer("unshare").Unshare(result, err)
		},
	}
}

func newPauseCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "pause <service>",
		Short: "Remove a service route from Tailscale without stopping the local process",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rt.app.Pause(cmd.Context(), app.PauseRequest{Start: rt.start(), Service: args[0]})
			return rt.renderer("pause").Pause(result, err)
		},
	}
}

func newResumeCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "resume <service>",
		Short: "Restore a paused service route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rt.app.Resume(cmd.Context(), app.ResumeRequest{Start: rt.start(), Service: args[0]})
			return rt.renderer("resume").Resume(result, err)
		},
	}
}

func newAddCommand(rt *runtime) *cobra.Command {
	var target, path, protocol string
	var public bool
	cmd := &cobra.Command{
		Use:   "add [name]",
		Short: "Add a service to pier.yaml",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			values := tui.AddServiceValues{Target: target, Path: path, Protocol: protocol, Public: public}
			if len(args) == 1 {
				values.Name = args[0]
			}
			if values.Name == "" || values.Target == "" {
				if !tui.StdioIsTTY() {
					return rt.renderer("service add").Error(fmt.Errorf("Pier service add requires a service name and --target"))
				}
				if err := tui.FillAddService(&values, existingNames(cmd.Context(), rt)); err != nil {
					if errors.Is(err, tui.ErrFormAborted) {
						return nil
					}
					return rt.renderer("service add").Error(err)
				}
			}
			result, err := rt.app.AddService(cmd.Context(), app.AddServiceRequest{
				Start:    rt.start(),
				Name:     values.Name,
				Target:   values.Target,
				Path:     values.Path,
				Public:   values.Public,
				Protocol: values.Protocol,
			})
			return rt.renderer("service add").Add(result, err)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "local host:port, for example localhost:4000")
	cmd.Flags().StringVar(&path, "path", "/", "Tailscale path")
	cmd.Flags().StringVar(&protocol, "protocol", "", "http or https")
	cmd.Flags().BoolVar(&public, "public", false, "expose through Tailscale Funnel")
	return cmd
}

func newServiceCommand(rt *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage services in pier.yaml",
	}
	cmd.AddCommand(newAddCommand(rt))
	return cmd
}

func existingNames(ctx context.Context, rt *runtime) []string {
	result, err := rt.app.Validate(ctx, app.ValidateRequest{Start: rt.start()})
	if err != nil && len(result.Config.Services) == 0 {
		return nil
	}
	names := make([]string, 0, len(result.Config.Services))
	for _, service := range result.Config.Services {
		names = append(names, service.Name)
	}
	return names
}

func newOpenCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "open <service>",
		Short: "Open the resolved service URL in the default browser",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rt.app.Open(cmd.Context(), app.OpenRequest{Start: rt.start(), Service: args[0]})
			return rt.renderer("open").URL("open", app.StatusResult{}.Project, result.URL, err)
		},
	}
}

func newTUICommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the interactive management interface",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTUI(cmd.Context(), rt)
		},
	}
}

func runTUI(ctx context.Context, rt *runtime) error {
	svc, ok := rt.app.(*app.Service)
	if !ok {
		return fmt.Errorf("Pier TUI requires the application service")
	}
	proj, err := project.Find(rt.start())
	if err != nil && !errors.Is(err, project.ErrNotFound) {
		return err
	}
	store, err := state.Open()
	if err != nil {
		return err
	}
	return tui.Run(ctx, svc, store, proj, tui.Options{Start: rt.start(), NoColor: rt.noColor})
}

func newCopyCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "copy <service>",
		Short: "Copy the resolved service URL to the clipboard",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := rt.app.Copy(cmd.Context(), app.CopyRequest{Start: rt.start(), Service: args[0]})
			return rt.renderer("copy").URL("copy", app.StatusResult{}.Project, result.URL, err)
		},
	}
}
