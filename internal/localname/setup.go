package localname

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Bethel-nz/pier/internal/certs"
	"github.com/Bethel-nz/pier/internal/localproxy"
	"github.com/Bethel-nz/pier/internal/trust"
)

// setupLease is how long one renewal of pier --setup keeps pier.local served.
// The command renews it every few seconds, so pier.local stops soon after the
// command ends, however it ends.
const setupLease = 15 * time.Second

func setupPath() (string, error) { return runtimePath("pierd.setup") }

// setupHeld reports whether pier --setup is running: its lease is fresh.
func setupHeld(now time.Time) bool {
	path, err := setupPath()
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && now.Sub(info.ModTime()) < setupLease
}

// HoldSetup renews the lease that keeps pier.local served with no project.
func (d *Directory) HoldSetup() error {
	path, err := setupPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return err
	}
	now := time.Now()
	return os.Chtimes(path, now, now)
}

// ReleaseSetup ends the lease. The daemon exits once nothing else needs it.
func (d *Directory) ReleaseSetup() {
	if path, err := setupPath(); err == nil {
		_ = os.Remove(path)
	}
}

// Setup gets this machine ready for other devices before any project has a
// .local name: it creates and trusts the CA, issues pier.local's
// certificate, and has the daemon serve the setup page there. The caller
// keeps calling HoldSetup while it should stay up.
func (d *Directory) Setup(ctx context.Context) (Report, error) {
	report := Report{Names: map[string]NameStatus{}}
	ca, created, err := certs.LoadOrCreateCA(d.caDir, d.now())
	if err != nil {
		return report, fmt.Errorf("Pier could not create its local CA: %w", err)
	}
	report.CAPath = ca.CertPath()
	report.CAFingerprint = certs.DisplayFingerprint(ca.Cert)
	report.CACreated = created
	if _, err := ca.EnsureLeaf(setupCertDir(d.caDir), []string{localproxy.SetupHost}, d.now()); err != nil {
		return report, fmt.Errorf("Pier could not issue a certificate for %s: %w", localproxy.SetupHost, err)
	}
	report.CATrusted = trust.IsTrusted(ca.Cert, ca.CertPath())
	if !report.CATrusted && d.Interactive {
		if err := trust.Install(ca.Cert, ca.CertPath()); err != nil {
			report.TrustError = err.Error()
		} else {
			report.CATrusted, report.TrustedNow = true, true
		}
	}
	report.Warnings = append(report.Warnings, browserStoreWarnings()...)
	if err := d.HoldSetup(); err != nil {
		return report, fmt.Errorf("Pier could not start serving %s: %w", localproxy.SetupHost, err)
	}

	beat, beatErr := readHeartbeat()
	running := beatErr == nil && beat.Fresh(d.now())
	if !running || beat.Build != buildID() {
		if running {
			_ = requestStop(beat.PID)
			d.waitGone(ctx, beat.PID)
		}
		if err := startDaemon(); err != nil {
			return report, err
		}
	}
	beat, err = d.waitSetup(ctx)
	d.fill(&report, beat, err == nil)
	return report, err
}

// waitSetup waits until the daemon reports pier.local live.
func (d *Directory) waitSetup(ctx context.Context) (Heartbeat, error) {
	deadline := d.now().Add(d.wait)
	for {
		beat, err := readHeartbeat()
		if err == nil && beat.Fresh(d.now()) && beat.Build == buildID() {
			if beat.SetupURL != "" {
				return beat, nil
			}
			if beat.Error != "" {
				return beat, errors.New(beat.Error)
			}
		}
		if d.now().After(deadline) {
			if err == nil && beat.MDNSError != "" {
				return beat, fmt.Errorf("Pier could not publish %s: %s", localproxy.SetupHost, beat.MDNSError)
			}
			return beat, fmt.Errorf("Pier is not serving %s yet; another device on this network may hold the name. See %s", localproxy.SetupHost, logHint())
		}
		select {
		case <-ctx.Done():
			return beat, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
