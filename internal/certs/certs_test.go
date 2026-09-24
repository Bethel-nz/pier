package certs

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func TestCAIsCreatedOnceAndReused(t *testing.T) {
	dir := t.TempDir()
	first, created, err := LoadOrCreateCA(dir, now)
	if err != nil || !created {
		t.Fatalf("LoadOrCreateCA() created=%v err=%v, want new CA", created, err)
	}
	second, created, err := LoadOrCreateCA(dir, now)
	if err != nil || created {
		t.Fatalf("second LoadOrCreateCA() created=%v err=%v, want reuse", created, err)
	}
	if first.Fingerprint() != second.Fingerprint() {
		t.Fatal("CA changed between loads")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, caKeyFile))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("CA key mode = %v (%v), want 0600", info.Mode().Perm(), err)
		}
	}
}

func TestExpiringCAIsReplaced(t *testing.T) {
	dir := t.TempDir()
	first, _, err := LoadOrCreateCA(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	later := now.Add(caValidity - 24*time.Hour)
	second, created, err := LoadOrCreateCA(dir, later)
	if err != nil || !created {
		t.Fatalf("LoadOrCreateCA(later) created=%v err=%v, want replacement", created, err)
	}
	if first.Fingerprint() == second.Fingerprint() {
		t.Fatal("expiring CA was not replaced")
	}
}

func TestCAOnlySignsLocalNames(t *testing.T) {
	ca, _, err := LoadOrCreateCA(t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	good, err := ca.Sign([]string{"my-app.local", "api.my-app.local"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := ca.Verify(good, now); err != nil {
		t.Fatalf("Verify(.local) = %v, want success", err)
	}
	for _, name := range []string{"example.com", "localhost", "my-app.localdomain"} {
		leaf, err := ca.Sign([]string{name}, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := ca.Verify(leaf, now); err == nil {
			t.Fatalf("Verify(%s) succeeded, want the name constraint to reject it", name)
		}
	}
}

func TestLeafIsReusedUntilNamesChange(t *testing.T) {
	ca, _, err := LoadOrCreateCA(t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), ".pier", "certs")

	changed, err := ca.EnsureLeaf(dir, []string{"web.local", "API.local."}, now)
	if err != nil || !changed {
		t.Fatalf("EnsureLeaf() changed=%v err=%v, want new leaf", changed, err)
	}
	changed, err = ca.EnsureLeaf(dir, []string{"api.local", "web.local"}, now)
	if err != nil || changed {
		t.Fatalf("EnsureLeaf(same names) changed=%v err=%v, want reuse", changed, err)
	}
	changed, err = ca.EnsureLeaf(dir, []string{"web.local"}, now)
	if err != nil || !changed {
		t.Fatalf("EnsureLeaf(fewer names) changed=%v err=%v, want reissue", changed, err)
	}

	pair, err := LoadLeaf(dir)
	if err != nil {
		t.Fatalf("LoadLeaf() = %v", err)
	}
	if len(pair.Certificate) != 2 {
		t.Fatalf("chain length = %d, want leaf + CA", len(pair.Certificate))
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, leafKeyFile))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("leaf key mode = %v (%v), want 0600", info.Mode().Perm(), err)
		}
	}
}

func TestLeafRenewsNearExpiryAndAfterNewCA(t *testing.T) {
	caDir := t.TempDir()
	ca, _, err := LoadOrCreateCA(caDir, now)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := ca.EnsureLeaf(dir, []string{"web.local"}, now); err != nil {
		t.Fatal(err)
	}
	changed, err := ca.EnsureLeaf(dir, []string{"web.local"}, now.Add(leafValidity-renewBefore+time.Hour))
	if err != nil || !changed {
		t.Fatalf("EnsureLeaf(near expiry) changed=%v err=%v, want renewal", changed, err)
	}

	other, _, err := LoadOrCreateCA(t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	changed, err = other.EnsureLeaf(dir, []string{"web.local"}, now)
	if err != nil || !changed {
		t.Fatalf("EnsureLeaf(new CA) changed=%v err=%v, want reissue", changed, err)
	}
}

func TestLeafCompletesATLSHandshake(t *testing.T) {
	ca, _, err := LoadOrCreateCA(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := ca.EnsureLeaf(dir, []string{"web.local"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	pair, err := LoadLeaf(dir)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.(*tls.Conn).Handshake()
			conn.Close()
		}
	}()
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	conn, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{RootCAs: roots, ServerName: "web.local"})
	if err != nil {
		t.Fatalf("handshake with web.local = %v, want trusted", err)
	}
	conn.Close()
}
