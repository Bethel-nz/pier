package cli

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Bethel-nz/pier/internal/runner"
)

// newLogsCommand shows what the project's run: commands printed, kept by
// pier up. It reads files; it does not need pier up to be running.
func newLogsCommand(rt *runtime) *cobra.Command {
	var follow bool
	var lines int
	cmd := &cobra.Command{
		Use:   "logs [service...]",
		Short: "Show the output of run: commands",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, names []string) error {
			plan, err := rt.app.RunPlan(rt.start())
			if err != nil {
				return rt.renderer("logs").Error(err)
			}
			dir := runner.LogDir(filepath.Join(plan.Project.Root, ".pier"))
			if len(runner.LogServices(dir)) == 0 {
				fmt.Fprintln(rt.stderr, "no run: command output yet; pier up records it")
				return nil
			}
			for _, name := range names {
				if _, err := os.Stat(filepath.Join(dir, name+".log")); err != nil {
					return rt.renderer("logs").Error(fmt.Errorf("no logs for %q; services with logs: %v", name, runner.LogServices(dir)))
				}
			}
			color := rt.colorFor(rt.stdout)
			if follow {
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
				defer stop()
				return runner.FollowLogs(ctx, rt.stdout, dir, names, lines, color)
			}
			entries, err := runner.ReadLogs(dir, names, lines)
			if err != nil {
				return rt.renderer("logs").Error(err)
			}
			services := names
			if len(services) == 0 {
				services = runner.LogServices(dir)
			}
			runner.PrintLogs(rt.stdout, entries, services, color)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new lines until Ctrl-C")
	cmd.Flags().IntVarP(&lines, "lines", "n", 100, "how many recent lines to show")
	return cmd
}
