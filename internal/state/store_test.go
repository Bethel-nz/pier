package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadMissingReturnsEmptyVersionedState(t *testing.T) {
	store := New(t.TempDir())

	got, err := store.Load("019f6429-aaaa-4bbb-8ccc-ddddeeeeffff")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := ProjectState{Version: CurrentVersion}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
}

func TestSaveLoadRoundTripPreservesOwnershipAndOverrides(t *testing.T) {
	store := New(t.TempDir())
	updatedAt := time.Date(2026, 9, 13, 15, 30, 0, 0, time.UTC)
	original := ProjectState{
		Version:    CurrentVersion,
		ProjectID:  "019f6429-aaaa-4bbb-8ccc-ddddeeeeffff",
		Name:       "greppa",
		Path:       "/Users/dev/greppa",
		DNSName:    "dev.tailnet.ts.net",
		ConfigHash: "sha256:abc123",
		Routes: []Route{
			{Service: "web", HTTPSPort: 8443, Path: "/"},
			{Service: "webhook", HTTPSPort: 443, Path: "/hooks"},
		},
		Overrides: map[string]bool{
			"api": true,
		},
		UpdatedAt: updatedAt,
	}

	if err := store.Save(original); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := store.Load(original.ProjectID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("Load() = %#v, want %#v", got, original)
	}
}

func TestFailedTemporaryWriteLeavesPreviousStateReadable(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	projectID := "019f6429-aaaa-4bbb-8ccc-ddddeeeeffff"
	previous := ProjectState{
		Version:   CurrentVersion,
		ProjectID: projectID,
		Name:      "previous",
		Path:      "/tmp/previous",
		Routes: []Route{
			{Service: "web", HTTPSPort: 8443, Path: "/"},
		},
		Overrides: map[string]bool{"web": true},
		UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	if err := store.Save(previous); err != nil {
		t.Fatalf("Save() previous error = %v", err)
	}

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod state directory: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o755)
	})

	err := store.Save(ProjectState{
		Version:   CurrentVersion,
		ProjectID: projectID,
		Name:      "replacement",
		Path:      "/tmp/replacement",
		UpdatedAt: time.Date(2026, 6, 7, 8, 9, 10, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("Save() error = nil, want failure while directory is not writable")
	}

	got, loadErr := store.Load(projectID)
	if loadErr != nil {
		t.Fatalf("Load() after failed Save error = %v", loadErr)
	}
	if !reflect.DeepEqual(got, previous) {
		t.Fatalf("Load() after failed Save = %#v, want previous %#v", got, previous)
	}
}

func TestSavedStatePermissionsDenyGroupAndWorldWrites(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	projectID := "019f6429-aaaa-4bbb-8ccc-ddddeeeeffff"
	if err := store.Save(ProjectState{
		Version:   CurrentVersion,
		ProjectID: projectID,
		Name:      "greppa",
		Path:      "/tmp/greppa",
		UpdatedAt: time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat state directory: %v", err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		t.Fatalf("state directory permissions = %#o, want no group/world write bits", info.Mode().Perm())
	}

	path := filepath.Join(dir, projectID+".json")
	info, err = os.Stat(path)
	if err != nil {
		t.Fatalf("stat state file: %v", err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		t.Fatalf("state file permissions = %#o, want no group/world write bits", info.Mode().Perm())
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state file permissions = %#o, want 0600", info.Mode().Perm())
	}
}

func TestLoadUnsupportedVersionReturnsMigrationError(t *testing.T) {
	dir := t.TempDir()
	projectID := "019f6429-aaaa-4bbb-8ccc-ddddeeeeffff"
	path := filepath.Join(dir, projectID+".json")
	payload, err := json.Marshal(map[string]any{
		"version":   CurrentVersion + 1,
		"projectId": projectID,
		"name":      "future",
		"path":      "/tmp/future",
	})
	if err != nil {
		t.Fatalf("marshal future state: %v", err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write future state: %v", err)
	}

	store := New(dir)
	_, err = store.Load(projectID)
	if err == nil {
		t.Fatal("Load() error = nil, want unsupported version error")
	}
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("Load() error = %v, want ErrUnsupportedVersion", err)
	}
	message := err.Error()
	if !strings.Contains(strings.ToLower(message), "migrat") {
		t.Fatalf("Load() error = %q, want actionable migration guidance", message)
	}
}

func TestPathForStaysInsideStoreRoot(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	id := "../escape"
	if err := store.Save(ProjectState{Version: CurrentVersion, ProjectID: id}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	escaped := filepath.Join(filepath.Dir(dir), "escape.json")
	if _, err := os.Stat(escaped); err == nil {
		t.Fatalf("Save() wrote outside the store root: %s", escaped)
	}
	got, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.ProjectID != id {
		t.Fatalf("Load() ProjectID = %q", got.ProjectID)
	}
}

func TestDeleteRemovesState(t *testing.T) {
	store := New(t.TempDir())
	projectID := "019f6429-aaaa-4bbb-8ccc-ddddeeeeffff"
	if err := store.Save(ProjectState{
		Version:   CurrentVersion,
		ProjectID: projectID,
		Name:      "greppa",
		Path:      "/tmp/greppa",
		UpdatedAt: time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	if err := store.Delete(projectID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	got, err := store.Load(projectID)
	if err != nil {
		t.Fatalf("Load() after Delete error = %v", err)
	}
	want := ProjectState{Version: CurrentVersion}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() after Delete = %#v, want %#v", got, want)
	}
}

func TestListReturnsSavedProjectsMostRecentlyUpdatedFirst(t *testing.T) {
	store := New(t.TempDir())
	older := ProjectState{
		Version:   CurrentVersion,
		ProjectID: "019f6429-aaaa-4bbb-8ccc-ddddeeeeffff",
		Name:      "older",
		Path:      "/tmp/older",
		UpdatedAt: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC),
	}
	newer := ProjectState{
		Version:   CurrentVersion,
		ProjectID: "019f6429-bbbb-4bbb-8ccc-ddddeeeeffff",
		Name:      "newer",
		Path:      "/tmp/newer",
		UpdatedAt: time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC),
	}
	if err := store.Save(older); err != nil {
		t.Fatalf("Save() older error = %v", err)
	}
	if err := store.Save(newer); err != nil {
		t.Fatalf("Save() newer error = %v", err)
	}

	got, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !reflect.DeepEqual(got, []ProjectState{newer, older}) {
		t.Fatalf("List() = %#v, want newer then older", got)
	}
}

func TestListSkipsUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	store := New(dir)
	project := ProjectState{
		Version:   CurrentVersion,
		ProjectID: "019f6429-aaaa-4bbb-8ccc-ddddeeeeffff",
		Name:      "pier",
		Path:      "/tmp/pier",
	}
	if err := store.Save(project); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("not state"), 0o600); err != nil {
		t.Fatalf("write unrelated file: %v", err)
	}

	got, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !reflect.DeepEqual(got, []ProjectState{project}) {
		t.Fatalf("List() = %#v, want only saved project", got)
	}
}
