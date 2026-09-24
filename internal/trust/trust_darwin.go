//go:build darwin

package trust

import (
	"bytes"
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

// remove takes the CA out of the login keychain. A CA that was never added,
// or is already gone, is removed already.
func remove(ca *x509.Certificate, caPath string) error {
	keychain := loginKeychain()
	if !inKeychain(ca, keychain) {
		return nil
	}
	_ = run("security", "remove-trusted-cert", caPath)
	return run("security", "delete-certificate", "-Z", thumbprint(ca), keychain)
}

// inKeychain reports whether keychain holds ca, matched by SHA-1 as security prints it.
func inKeychain(ca *x509.Certificate, keychain string) bool {
	out, err := exec.Command("security", "find-certificate", "-a", "-Z", keychain).Output()
	return err == nil && bytes.Contains(out, []byte(thumbprint(ca)))
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
