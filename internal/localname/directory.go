package localname

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Bethel-nz/pier/internal/certs"
	"github.com/Bethel-nz/pier/internal/state"
	"github.com/Bethel-nz/pier/internal/trust"
)

// Report is what pier up and pier status show about local names.
type Report struct {
	Running   bool
	HTTPSPort int
	Names     map[string]NameStatus
	CAPath    string
	// CAFingerprint is the SHA-256 other devices compare before trusting the CA.
	CAFingerprint string
	CATrusted     bool
	CACreated     bool
	// TrustedNow is set when this sync added the CA to the trust store.
	TrustedNow bool
	TrustError string
	CertDir    string
	// CertIssued is set when this sync wrote a new or renewed certificate.
	CertIssued bool
	// Autostart is set when the daemon starts at login for some project.
	Autostart bool
	// APIURL is the loopback dashboard API, while the daemon runs.
	APIURL   string
	Warnings []string
}

// URL is the browser address for name, or "" when it is not being served.
func (r Report) URL(name string) string {
	status, ok := r.Names[name]
	if !r.Running || !ok || status.State != StateLive {
		return ""
	}
	return Heartbeat{HTTPSPort: r.HTTPSPort}.URL(name)
}

// State is the served state of name: live, probing, conflict, … or "down".
func (r Report) State(name string) string {
	if !r.Running {
		return "down"
	}
	if status, ok := r.Names[name]; ok {
		return status.State
	}
	return "down"
}

// Directory is the pier up side of local names: it issues certificates,
// trusts the CA, and makes sure the daemon serves what state declares.
type Directory struct {
	projects *state.Store
	// Interactive allows OS trust prompts (password dialog, sudo).
	Interactive bool
	caDir       string
	now         func() time.Time
	wait        time.Duration
	untrust     func(*x509.Certificate, string) error
	loginItem   func(exe string, on bool) error
}

// NewDirectory manages local names for projects saved in store.
func NewDirectory(store *state.Store) *Directory {
	caDir, _ := certs.DefaultCADir()
	return &Directory{
		projects: store, caDir: caDir, now: time.Now, wait: 6 * time.Second,
		untrust: trust.Remove, loginItem: setLoginItem,
	}
}

// Sync is called after pier up, pause, resume, or down saved state. names are
// the project's served names. Certificates and trust are handled first, then
// the daemon is started (or told to stop when no project has names left).
func (d *Directory) Sync(ctx context.Context, root string, names []string, settings state.LocalSettings) (Report, error) {
	report := Report{Names: map[string]NameStatus{}}
	if len(names) > 0 {
		if err := d.prepare(root, names, settings, &report); err != nil {
			return report, err
		}
	}
	saved, err := d.projects.List()
	if err != nil {
		return report, fmt.Errorf("Pier could not read local domains: %w", err)
	}
	routes, _ := Routes(saved)
	// The login item exists exactly while some project asks for autostart.
	report.Autostart = wantsAutostart(saved)
	if err := d.setAutostart(report.Autostart); err != nil {
		report.Warnings = append(report.Warnings, "Pier could not update autostart: "+err.Error())
	}
	beat, beatErr := readHeartbeat()
	running := beatErr == nil && beat.Fresh(d.now())
	if len(routes) == 0 {
		if running {
			_ = requestStop(beat.PID)
		}
		sweepPublishers() // records a crashed daemon left with the system responder
		return report, nil
	}
	if !running || beat.Build != buildID() {
		if running {
			_ = requestStop(beat.PID)
			d.waitGone(ctx, beat.PID)
		}
		if err := startDaemon(); err != nil {
			return report, err
		}
	}
	beat, err = d.waitServing(ctx, names)
	d.fill(&report, beat, err == nil)
	return report, err
}

// Status reads the daemon's heartbeat without changing anything.
func (d *Directory) Status() Report {
	report := Report{Names: map[string]NameStatus{}}
	if ca, err := certs.LoadCA(d.caDir, d.now()); err == nil {
		report.CAPath = ca.CertPath()
		report.CAFingerprint = certs.DisplayFingerprint(ca.Cert)
		report.CATrusted = trust.IsTrusted(ca.Cert, ca.CertPath())
		report.Warnings = append(report.Warnings, browserStoreWarnings()...)
	}
	if saved, err := d.projects.List(); err == nil {
		report.Autostart = wantsAutostart(saved)
	}
	beat, err := readHeartbeat()
	d.fill(&report, beat, err == nil && beat.Fresh(d.now()))
	return report
}

// browserStoreWarnings explains browsers Pier cannot update by itself.
func browserStoreWarnings() []string {
	if !trust.BrowserStoresNeedCertutil() {
		return nil
	}
	return []string{"Chrome and Firefox here use their own certificate store. Install NSS tools (libnss3-tools or nss-tools), then run pier trust"}
}

// Trust installs the CA, creating it first if needed.
func (d *Directory) Trust() (string, error) {
	ca, _, err := certs.LoadOrCreateCA(d.caDir, d.now())
	if err != nil {
		return "", err
	}
	return ca.CertPath(), trust.Install(ca.Cert, ca.CertPath())
}

// Untrust removes the CA from the trust store.
func (d *Directory) Untrust() (string, error) {
	ca, err := certs.LoadCA(d.caDir, d.now())
	if err != nil {
		return "", fmt.Errorf("Pier has no local CA to remove: %w", err)
	}
	return ca.CertPath(), trust.Remove(ca.Cert, ca.CertPath())
}

func (d *Directory) prepare(root string, names []string, settings state.LocalSettings, report *Report) error {
	if settings.CertFile != "" {
		// Bring-your-own certificate: no CA, no issuing, no trust prompt.
		if err := certs.CheckPair(settings.CertFile, settings.KeyFile, names); err != nil {
			return fmt.Errorf("Pier cannot serve with local.tls: %w", err)
		}
		report.CertDir = filepath.Dir(settings.CertFile)
		report.CATrusted = true
		return nil
	}
	ca, created, err := certs.LoadOrCreateCA(d.caDir, d.now())
	if err != nil {
		return fmt.Errorf("Pier could not create its local CA: %w", err)
	}
	report.CAPath = ca.CertPath()
	report.CAFingerprint = certs.DisplayFingerprint(ca.Cert)
	report.CACreated = created
	if err := ignorePierDir(root); err != nil {
		return err
	}
	report.CertDir = certs.LeafDir(root)
	issued, err := ca.EnsureLeaf(report.CertDir, names, d.now())
	if err != nil {
		return fmt.Errorf("Pier could not issue a certificate for %s: %w", root, err)
	}
	report.CertIssued = issued
	report.Warnings = append(report.Warnings, browserStoreWarnings()...)
	report.CATrusted = trust.IsTrusted(ca.Cert, ca.CertPath())
	if report.CATrusted {
		return nil
	}
	if !d.Interactive {
		return nil
	}
	if err := trust.Install(ca.Cert, ca.CertPath()); err != nil {
		report.TrustError = err.Error()
		return nil
	}
	report.CATrusted = true
	report.TrustedNow = true
	return nil
}

// ignorePierDir keeps certificate keys out of git: in a repository, .pier/
// is added to .gitignore before any key is written.
func ignorePierDir(root string) error {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return nil
	}
	if exec.Command("git", "-C", root, "check-ignore", "-q", ".pier/certs/key.pem").Run() == nil {
		return nil
	}
	path := filepath.Join(root, ".gitignore")
	contents, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	addition := ".pier/\n"
	if len(contents) > 0 && contents[len(contents)-1] != '\n' {
		addition = "\n" + addition
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("Pier will not write certificate keys until .pier/ is ignored by git: %w", err)
	}
	defer file.Close()
	_, err = file.WriteString(addition)
	return err
}

// waitServing waits until the daemon reports every name past probing.
func (d *Directory) waitServing(ctx context.Context, names []string) (Heartbeat, error) {
	deadline := d.now().Add(d.wait)
	var last Heartbeat
	for {
		beat, err := readHeartbeat()
		if err == nil && beat.Fresh(d.now()) && beat.Build == buildID() {
			last = beat
			if beat.Error != "" {
				return beat, errors.New(beat.Error)
			}
			if settled(beat, names) {
				return beat, nil
			}
		} else if err == nil && beat.Error != "" && beat.Build == buildID() {
			return beat, errors.New(beat.Error)
		}
		if d.now().After(deadline) {
			if last.PID != 0 {
				return last, nil
			}
			return last, fmt.Errorf("Pier started its local-name daemon but it did not report in; see %s", logHint())
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func settled(beat Heartbeat, names []string) bool {
	byName := map[string]string{}
	for _, status := range beat.Names {
		byName[status.Name] = status.State
	}
	for _, name := range names {
		state, ok := byName[name]
		if !ok || state == StateProbing {
			return false
		}
	}
	return true
}

func (d *Directory) waitGone(ctx context.Context, pid int) {
	deadline := d.now().Add(3 * time.Second)
	for d.now().Before(deadline) {
		beat, err := readHeartbeat()
		if err != nil || beat.PID != pid {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (d *Directory) fill(report *Report, beat Heartbeat, running bool) {
	report.Running = running
	if !running {
		return
	}
	report.HTTPSPort = beat.HTTPSPort
	if beat.APIPort != 0 {
		report.APIURL = fmt.Sprintf("http://127.0.0.1:%d/api", beat.APIPort)
	}
	report.Warnings = append(report.Warnings, beat.Warnings...)
	if beat.MDNSError != "" {
		report.Warnings = append(report.Warnings, beat.MDNSError)
	}
	for _, status := range beat.Names {
		report.Names[status.Name] = status
	}
}

func logHint() string {
	path, err := logPath()
	if err != nil {
		return "the pier daemon log"
	}
	return path
}

// startDaemon launches `pier locald` detached, logging to pierd.log.
func startDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("Pier could not start local name serving: %w", err)
	}
	path, err := logPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	command := exec.Command(exe, "locald")
	command.Stdout = logFile
	command.Stderr = logFile
	detach(command)
	if err := command.Start(); err != nil {
		return fmt.Errorf("Pier could not start local name serving: %w", err)
	}
	return command.Process.Release()
}
