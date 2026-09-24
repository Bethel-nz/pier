package trust

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bethel-nz/pier/internal/certs"
)

func TestInstallWritesTheAnchorAndMarker(t *testing.T) {
	anchors := filepath.Join(t.TempDir(), "anchors")
	savedStores, savedRun := stores, run
	t.Cleanup(func() { stores, run = savedStores, savedRun })
	stores = []store{{dir: anchors, update: "true"}}
	var calls [][]string
	run = func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		if name == "sudo" && args[0] == "install" {
			if err := os.MkdirAll(anchors, 0o755); err != nil {
				return err
			}
			contents, err := os.ReadFile(args[len(args)-2])
			if err != nil {
				return err
			}
			return os.WriteFile(args[len(args)-1], contents, 0o644)
		}
		return nil
	}
	t.Setenv("HOME", t.TempDir())

	ca, _, err := certs.LoadOrCreateCA(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if IsTrusted(ca.Cert, ca.CertPath()) {
		t.Fatal("new CA reported as trusted")
	}
	if err := Install(ca.Cert, ca.CertPath()); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	if !IsTrusted(ca.Cert, ca.CertPath()) {
		t.Fatal("installed CA not reported as trusted")
	}
	if _, err := os.Stat(filepath.Join(anchors, anchorName)); err != nil {
		t.Fatalf("anchor not written: %v", err)
	}
	if len(calls) == 0 {
		t.Fatal("update command never ran")
	}

	if err := Remove(ca.Cert, ca.CertPath()); err != nil {
		t.Fatalf("Remove() = %v", err)
	}
	if readMarker(ca.CertPath()) != "" {
		t.Fatal("marker survived Remove")
	}
}
