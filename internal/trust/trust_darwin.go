//go:build darwin

package trust

import (
	"crypto/x509"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// systemTrusts asks the macOS trust evaluator whether the CA is trusted.
func systemTrusts(_ *x509.Certificate, caPath string) bool {
	return quiet("security", "verify-cert", "-c", caPath, "-L", "-p", "ssl")
}

// install adds the CA to the login keychain as a trusted root. macOS shows its
// own password dialog; no sudo is needed.
func install(_ *x509.Certificate, caPath string) error {
	return run("security", "add-trusted-cert", "-r", "trustRoot", "-k", loginKeychain(), caPath)
}

func remove(ca *x509.Certificate, caPath string) error {
	_ = run("security", "remove-trusted-cert", caPath)
	return run("security", "delete-certificate", "-Z", thumbprint(ca), loginKeychain())
}

var quoted = regexp.MustCompile(`"(.+)"`)

func loginKeychain() string {
	if out, err := exec.Command("security", "default-keychain").Output(); err == nil {
		if match := quoted.FindSubmatch(out); match != nil {
			return string(match[1])
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Keychains", "login.keychain-db")
}

// BrowserStoresNeedCertutil is false on macOS: browsers use the keychain.
func BrowserStoresNeedCertutil() bool { return false }

// browsersTrust is true: browsers here use the system store.
func browsersTrust(string) bool { return true }
