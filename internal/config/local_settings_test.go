package config

import (
	"os"
	"path/filepath"
	"testing"
)

func loadProject(t *testing.T, yaml string) Project {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pier.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := Load(path)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	project, err := Normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	return project
}

const service = "services:\n  web:\n    target: localhost:3000\n    local: myapp.local\n"

func TestLocalSettingsDefaultToLANWithPierCertificate(t *testing.T) {
	project := loadProject(t, "version: 1\nname: demo\n"+service)
	if !project.Local.LAN || project.Local.Autostart || project.Local.CertFile != "" {
		t.Fatalf("Local = %+v, want LAN on, no autostart, Pier's certificate", project.Local)
	}
}

func TestLocalSettingsAreRead(t *testing.T) {
	project := loadProject(t, "version: 1\nname: demo\nlocal:\n  lan: false\n  autostart: true\n  tls:\n    cert: certs/dev.pem\n    key: certs/dev-key.pem\n"+service)
	want := Local{LAN: false, Autostart: true, CertFile: "certs/dev.pem", KeyFile: "certs/dev-key.pem"}
	if project.Local != want {
		t.Fatalf("Local = %+v, want %+v", project.Local, want)
	}
	if errors := Validate(project); len(errors) != 0 {
		t.Fatalf("Validate() = %v", errors)
	}
}

func TestTLSNeedsBothFiles(t *testing.T) {
	project := loadProject(t, "version: 1\nname: demo\nlocal:\n  tls:\n    cert: certs/dev.pem\n"+service)
	errors := Validate(project)
	if len(errors) != 1 || errors[0].Field != "local.tls" {
		t.Fatalf("Validate() = %v, want one local.tls error", errors)
	}
}
