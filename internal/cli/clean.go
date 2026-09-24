package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Bethel-nz/pier/internal/localname"
	"github.com/Bethel-nz/pier/internal/project"
	"github.com/Bethel-nz/pier/internal/state"
)

func newCleanCommand(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "clean",
		Short: "Remove Pier's local names, certificates, CA, and daemon from this machine",
		Long: "Stop the local-name daemon, remove Pier's CA from the trust store, delete the CA and every\n" +
			"project's .pier/certs, and forget saved local names. Tailscale routes are not touched;\n" +
			"run pier down in a project to remove those. pier up sets everything up again.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			directory := rt.local
			if directory == nil {
				store, err := state.Open()
				if err != nil {
					return rt.renderer("clean").Error(err)
				}
				directory = localname.NewDirectory(store)
			}
			report := directory.Clean(cmd.Context())
			var err error
			if len(report.Errors) > 0 {
				err = errors.New("Pier could not finish cleaning: " + strings.Join(report.Errors, "; "))
			}
			if rt.json {
				return rt.renderer("clean").JSONData("clean", project.Context{}, report, err)
			}
			writeClean(rt, report)
			if err != nil {
				return rt.renderer("clean").Error(err)
			}
			return nil
		},
	}
}

func writeClean(rt *runtime, report localname.CleanReport) {
	line := func(format string, args ...any) { fmt.Fprintf(rt.stdout, format+"\n", args...) }
	if report.StoppedDaemon {
		line("stopped      local-name daemon")
	}
	if report.Untrusted != "" {
		line("untrusted    %s", report.Untrusted)
	}
	if report.RemovedCA != "" {
		line("removed      %s", report.RemovedCA)
	}
	for _, dir := range report.RemovedCerts {
		line("removed      %s", dir)
	}
	if len(report.ClearedNames) > 0 {
		line("forgot       %s", strings.Join(report.ClearedNames, ", "))
	}
	if !report.StoppedDaemon && report.Untrusted == "" && report.RemovedCA == "" && len(report.RemovedCerts) == 0 && len(report.ClearedNames) == 0 {
		line("nothing to clean")
	}
	line("Tailscale routes are unchanged; run pier down in a project to remove them.")
}
