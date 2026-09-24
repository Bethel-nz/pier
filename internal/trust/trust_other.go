//go:build !darwin && !linux && !windows

package trust

import "crypto/x509"

func systemTrusts(*x509.Certificate, string) bool { return false }

func install(*x509.Certificate, string) error { return ErrUnsupported }

func remove(*x509.Certificate, string) error { return ErrUnsupported }

// BrowserStoresNeedCertutil is false where Pier does not manage trust.
func BrowserStoresNeedCertutil() bool { return false }

// browsersTrust is true: browsers here use the system store.
func browsersTrust(string) bool { return true }
