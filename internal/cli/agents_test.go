package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentsPrintsSetupPrompt(t *testing.T) {
	stdout, _, err := runCLI(t, &fakeApp{}, "agents")
	if err != nil {
		t.Fatalf("agents error = %v", err)
	}
	for _, want := range []string{
		"Configure this repository with Pier",
		"version: 1",
		"target: localhost:3000",
		"protocol: http",
		"public: false",
		"pier service add --help",
		"pier validate",
		"pier plan",
		"https://github.com/Bethel-nz/pier/tree/main/examples/configs",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("agents output missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "project documentation") {
		t.Fatalf("agents output depends on ambiguous external documentation:\n%s", stdout)
	}
}

func TestAgentsWriteCreatesAgentsFile(t *testing.T) {
	dir := t.TempDir()
	withinDirectory(t, dir)

	stdout, _, err := runCLI(t, &fakeApp{}, "agents", "--write")
	if err != nil {
		t.Fatalf("agents --write error = %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if !strings.Contains(string(contents), "Configure this repository with Pier") {
		t.Fatalf("AGENTS.md contents:\n%s", contents)
	}
	if !strings.Contains(stdout, "AGENTS.md") {
		t.Fatalf("agents --write output = %q", stdout)
	}
}

func TestAgentsWriteDoesNotOverwriteExistingFile(t *testing.T) {
	dir := t.TempDir()
	withinDirectory(t, dir)
	path := filepath.Join(dir, "AGENTS.md")
	const original = "# Existing instructions\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, err := runCLI(t, &fakeApp{}, "agents", "--write")
	if err == nil {
		t.Fatal("agents --write error = nil, want overwrite refusal")
	}
	contents, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(contents) != original {
		t.Fatalf("existing AGENTS.md changed:\n%s", contents)
	}
	if !strings.Contains(stderr, "overwrite existing") {
		t.Fatalf("agents --write stderr = %q", stderr)
	}
}

func withinDirectory(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}
