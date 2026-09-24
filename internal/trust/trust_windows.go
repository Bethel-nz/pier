//go:build windows

package trust

import "crypto/x509"

// systemTrusts checks the current user's Root store.
func systemTrusts(ca *x509.Certificate, _ string) bool {
	return quiet("certutil", "-user", "-verifystore", "Root", thumbprint(ca))
}

// install adds the CA to the current user's Root store. Windows shows a
// confirmation dialog; no administrator rights are needed.
func install(_ *x509.Certificate, caPath string) error {
	return run("certutil", "-user", "-addstore", "Root", caPath)
}

func remove(ca *x509.Certificate, _ string) error {
	return run("certutil", "-user", "-delstore", "Root", thumbprint(ca))
}

// BrowserStoresNeedCertutil is false on Windows: browsers use the system store.
func BrowserStoresNeedCertutil() bool { return false }
