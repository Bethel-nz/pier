package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddServiceInsertsValidatedService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pier.yaml")
	original := "version: 1\nname: demo\n\n# local app\nservices:\n  web:\n    target: localhost:3000\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	public := false
	err := AddService(path, "api", Service{Target: "localhost:4000", Path: "/api", Public: PublicFlag(public)})
	if err != nil {
		t.Fatalf("AddService() error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, "# local app") {
		t.Fatalf("AddService() dropped comments:\n%s", text)
	}
	if !strings.Contains(text, "api:") || !strings.Contains(text, "target: localhost:4000") {
		t.Fatalf("AddService() missing new service:\n%s", text)
	}

	raw, err := Load(path)
	if err != nil {
		t.Fatalf("Load() after AddService error = %v", err)
	}
	if _, ok := raw.Services["api"]; !ok {
		t.Fatalf("Load() services = %#v, want api", raw.Services)
	}
}

func TestAddServiceRejectsDuplicateAndInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pier.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nname: demo\nservices:\n  web:\n    target: localhost:3000\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	public := false
	if err := AddService(path, "web", Service{Target: "localhost:4000", Public: PublicFlag(public)}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate error = %v, want already exists", err)
	}
	if err := AddService(path, "API", Service{Target: "localhost:4000", Public: PublicFlag(public)}); err == nil {
		t.Fatal("invalid name error = nil")
	}
	if err := AddService(path, "api", Service{Target: "example.com:4000", Public: PublicFlag(public)}); err == nil {
		t.Fatal("invalid target error = nil")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "api:") {
		t.Fatalf("rejected AddService still mutated pier.yaml:\n%s", got)
	}
}
