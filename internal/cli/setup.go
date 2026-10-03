package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Bethel-nz/pier/internal/localname"
	"github.com/Bethel-nz/pier/internal/project"
	"github.com/Bethel-nz/pier/internal/render"
)

// setupRenew is how often pier --setup renews its hold on pier.local.
const setupRenew = 5 * time.Second

// runSetup is pier --setup: it readies this machine and serves
// pier.local/setup, so every other device can trust Pier, until Ctrl-C.
func runSetup(ctx context.Context, rt *runtime) error {
	if rt.local == nil {
		return rt.renderer("setup").Error(errors.New("Pier cannot serve its setup page from here"))
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer rt.local.ReleaseSetup()

	report, err := rt.local.Setup(ctx)
	if err != nil {
		return rt.renderer("setup").Error(err)
	}
	if rt.json {
		if err := rt.renderer("setup").URL("setup", project.Context{}, report.SetupURL, nil); err != nil {
			return err
		}
	} else {
		writeSetup(rt, report)
	}

	tick := time.NewTicker(setupRenew)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			if !rt.json {
				fmt.Fprintln(rt.stdout, "\nsetup        stopped serving pier.local; run pier --setup again for another device")
			}
			return nil
		case <-tick.C:
			_ = rt.local.HoldSetup()
		}
	}
}

func writeSetup(rt *runtime, report localname.Report) {
	out := rt.stdout
	if report.CACreated {
		fmt.Fprintf(out, "ca           created %s\n", report.CAPath)
	}
	switch {
	case report.TrustedNow:
		fmt.Fprintln(out, "trusted      Pier Local CA added to this machine's trust store")
	case report.TrustError != "":
		fmt.Fprintf(out, "trust        Pier could not trust its CA here (%s). Run pier trust to retry\n", report.TrustError)
	case !report.CATrusted:
		fmt.Fprintln(out, "trust        Pier Local CA is not trusted on this machine yet. Run pier trust")
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(out, "warning      %s\n", warning)
	}
	fmt.Fprintln(out)
	if err := render.QR(out, report.SetupURL, !rt.noColor); err == nil {
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "setup        open %s on each phone, tablet, or laptop (or scan the code)\n", report.SetupURL)
	fmt.Fprintf(out, "             check it shows SHA-256 %s\n", report.CAFingerprint)
	fmt.Fprintln(out, "             serving until Ctrl-C. Trusting once covers every .local name Pier serves")
}
