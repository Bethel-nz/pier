//go:build linux

package trust

import (
	"crypto/x509"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const anchorName = "pier-local-ca.crt"

// store is one distro family's CA anchor directory and refresh command.
type store struct {
	dir    string
	update string
}

var stores = []store{
	{dir: "/usr/local/share/ca-certificates", update: "update-ca-certificates"},   // Debian, Ubuntu
	{dir: "/etc/pki/ca-trust/source/anchors", update: "update-ca-trust"},          // Fedora, RHEL
	{dir: "/etc/ca-certificates/trust-source/anchors", update: "update-ca-trust"}, // Arch
	{dir: "/etc/pki/trust/anchors", update: "update-ca-certificates"},             // openSUSE
}

func systemStore() (store, error) {
	for _, s := range stores {
		if _, err := exec.LookPath(s.update); err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Dir(s.dir)); err == nil {
			return s, nil
		}
	}
	return store{}, fmt.Errorf("%w: no update-ca-certificates or update-ca-trust found", ErrUnsupported)
}

func systemTrusts(_ *x509.Certificate, caPath string) bool {
	s, err := systemStore()
	if err != nil {
		return false
	}
	contents, err := os.ReadFile(caPath)
	return err == nil && sameFile(filepath.Join(s.dir, anchorName), contents)
}

func install(ca *x509.Certificate, caPath string) error {
	s, err := systemStore()
	if err != nil {
		return err
	}
	dest := filepath.Join(s.dir, anchorName)
	if os.Geteuid() == 0 {
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			return err
		}
		contents, err := os.ReadFile(caPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dest, contents, 0o644); err != nil {
			return err
		}
		if err := run(s.update); err != nil {
			return fmt.Errorf("%s failed: %w", s.update, err)
		}
	} else {
		if err := run("sudo", "install", "-D", "-m", "0644", caPath, dest); err != nil {
			return fmt.Errorf("Pier could not copy its CA into %s: %w", s.dir, err)
		}
		if err := run("sudo", s.update); err != nil {
			return fmt.Errorf("%s failed: %w", s.update, err)
		}
	}
	addToBrowsers(caPath)
	if isWSL() {
		_ = installWindowsFromWSL(ca, caPath)
	}
	return nil
}

func remove(ca *x509.Certificate, _ string) error {
	s, err := systemStore()
	if err != nil {
		return err
	}
	dest := filepath.Join(s.dir, anchorName)
	removeFromBrowsers()
	if isWSL() {
		_ = run("certutil.exe", "-user", "-delstore", "Root", thumbprint(ca))
	}
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		return nil
	}
	if os.Geteuid() == 0 {
		if err := os.Remove(dest); err != nil {
			return err
		}
		return run(s.update)
	}
	if err := run("sudo", "rm", "-f", dest); err != nil {
		return err
	}
	return run("sudo", s.update)
}

// Chrome and Firefox on Linux read NSS databases, not the system anchors.
// NSS's certutil is optional; when it is present Pier uses it, no sudo needed.
const nssNickname = "Pier Local CA"

func nssDatabases() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var dbs []string
	if _, err := os.Stat(filepath.Join(home, ".pki", "nssdb", "cert9.db")); err == nil {
		dbs = append(dbs, filepath.Join(home, ".pki", "nssdb"))
	}
	for _, pattern := range []string{
		filepath.Join(home, ".mozilla", "firefox", "*"),
		filepath.Join(home, "snap", "firefox", "common", ".mozilla", "firefox", "*"),
	} {
		profiles, _ := filepath.Glob(pattern)
		for _, profile := range profiles {
			if _, err := os.Stat(filepath.Join(profile, "cert9.db")); err == nil {
				dbs = append(dbs, profile)
			}
		}
	}
	return dbs
}

// BrowserStoresNeedCertutil reports NSS databases Pier could not update
// because NSS's certutil is not installed.
func BrowserStoresNeedCertutil() bool {
	if _, err := exec.LookPath("certutil"); err == nil {
		return false
	}
	return len(nssDatabases()) > 0
}

func addToBrowsers(caPath string) {
	if _, err := exec.LookPath("certutil"); err != nil {
		return
	}
	for _, db := range nssDatabases() {
		_ = quietRun("certutil", "-d", "sql:"+db, "-A", "-t", "C,,", "-n", nssNickname, "-i", caPath)
	}
}

func removeFromBrowsers() {
	if _, err := exec.LookPath("certutil"); err != nil {
		return
	}
	for _, db := range nssDatabases() {
		_ = quietRun("certutil", "-d", "sql:"+db, "-D", "-n", nssNickname)
	}
}

func quietRun(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	release, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(release)), "microsoft")
}

// installWindowsFromWSL trusts the CA for Windows browsers too.
func installWindowsFromWSL(_ *x509.Certificate, caPath string) error {
	out, err := exec.Command("wslpath", "-w", caPath).Output()
	if err != nil {
		return err
	}
	return run("certutil.exe", "-user", "-addstore", "Root", strings.TrimSpace(string(out)))
}
