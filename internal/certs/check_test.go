package certs

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckPairAcceptsACoveringCertificate(t *testing.T) {
	ca, _, err := LoadOrCreateCA(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := ca.EnsureLeaf(dir, []string{"myapp.local", "api.myapp.local"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cert, key := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := CheckPair(cert, key, []string{"myapp.local"}); err != nil {
		t.Fatalf("CheckPair() = %v", err)
	}
	err = CheckPair(cert, key, []string{"myapp.local", "shop.local"})
	if err == nil || !strings.Contains(err.Error(), "does not cover shop.local") {
		t.Fatalf("CheckPair(uncovered name) = %v", err)
	}
	if err := CheckPair(filepath.Join(dir, "missing.pem"), key, nil); err == nil {
		t.Fatal("CheckPair(missing file) succeeded")
	}
}
