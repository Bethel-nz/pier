package localname

import (
	"context"
	"crypto/x509"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Bethel-nz/pier/internal/state"
)

func TestCleanListsOrphanedDNSOnceThenForgetsIt(t *testing.T) {
	useConfigDir(t)
	store := state.New(filepath.Join(t.TempDir(), "projects"))
	d := NewDirectory(store)
	d.caDir = filepath.Join(t.TempDir(), "ca")
	d.untrust = func(*x509.Certificate, string) error { return nil }
	if err := store.Save(state.ProjectState{ProjectID: "p1", Name: "demo", Tunnel: &state.Tunnel{
		ID: "6f1c", Name: "pier-demo-p1", Routed: []string{"api.example.com", "old.example.com"}, Orphans: []string{"old.example.com"},
	}}); err != nil {
		t.Fatal(err)
	}

	report := d.Clean(context.Background())
	want := []OrphanedDNS{{Hostname: "old.example.com", Tunnel: "pier-demo-p1", Project: "demo"}}
	if !reflect.DeepEqual(report.OrphanedDNS, want) {
		t.Fatalf("orphans = %+v, want %+v", report.OrphanedDNS, want)
	}
	saved, err := store.Load("p1")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Tunnel == nil || len(saved.Tunnel.Orphans) != 0 || !reflect.DeepEqual(saved.Tunnel.Routed, []string{"api.example.com"}) {
		t.Fatalf("tunnel after clean = %+v, want the orphan forgotten and the rest kept", saved.Tunnel)
	}
	if again := d.Clean(context.Background()); len(again.OrphanedDNS) != 0 {
		t.Fatalf("second clean listed %+v again", again.OrphanedDNS)
	}
}
