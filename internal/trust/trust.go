// Package trust adds the Pier CA to this machine's trust store using only
// tools the operating system ships with.
package trust

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // Windows and macOS identify certificates by SHA-1 thumbprint.
	"crypto/x509"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Bethel-nz/pier/internal/certs"
)

// ErrUnsupported means Pier cannot manage trust on this operating system.
var ErrUnsupported = errors.New("Pier cannot add certificates to this system's trust store")

const markerFile = "trusted"

// IsTrusted reports whether the CA at caPath is in the system trust store.
// A marker holding the CA fingerprint skips the slower OS check.
func IsTrusted(ca *x509.Certificate, caPath string) bool {
	if readMarker(caPath) == certs.Fingerprint(ca) {
		return true
	}
	if systemTrusts(ca, caPath) {
		_ = writeMarker(caPath, certs.Fingerprint(ca))
		return true
	}
	return false
}

// Install adds the CA to the trust store. It may show an OS prompt or ask
// for a sudo password, so callers only run it with a terminal attached.
func Install(ca *x509.Certificate, caPath string) error {
	if err := install(ca, caPath); err != nil {
		return err
	}
	return writeMarker(caPath, certs.Fingerprint(ca))
}

// Remove takes the CA out of the trust store.
func Remove(ca *x509.Certificate, caPath string) error {
	_ = os.Remove(filepath.Join(filepath.Dir(caPath), markerFile))
	return remove(ca, caPath)
}

func readMarker(caPath string) string {
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(caPath), markerFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(contents))
}

func writeMarker(caPath, fingerprint string) error {
	return os.WriteFile(filepath.Join(filepath.Dir(caPath), markerFile), []byte(fingerprint+"\n"), 0o600)
}

func thumbprint(ca *x509.Certificate) string {
	sum := sha1.Sum(ca.Raw) //nolint:gosec
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// run executes a trust command attached to the terminal so OS prompts work.
var run = func(name string, args ...string) error {
	command := exec.Command(name, args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	return command.Run()
}

// quiet executes a check command and reports only success.
var quiet = func(name string, args ...string) bool {
	return exec.Command(name, args...).Run() == nil
}

func sameFile(path string, contents []byte) bool {
	existing, err := os.ReadFile(path)
	return err == nil && bytes.Equal(bytes.TrimSpace(existing), bytes.TrimSpace(contents))
}
