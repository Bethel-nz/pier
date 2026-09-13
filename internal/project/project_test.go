package project

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"pier/internal/config"
)

func TestFindReturnsNearestAncestorConfig(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	child := filepath.Join(parent, "child")
	writeFile(t, filepath.Join(root, "pier.yaml"), "name: root\n")
	writeFile(t, filepath.Join(parent, "pier.yaml"), "name: parent\n")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("create child: %v", err)
	}

	context, err := Find(child)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if context.Root != parent {
		t.Errorf("Find() root = %q, want %q", context.Root, parent)
	}
	if context.ConfigPath != filepath.Join(parent, "pier.yaml") {
		t.Errorf("Find() config path = %q, want nearest parent config", context.ConfigPath)
	}
}

func TestFindUsesExplicitConfigPath(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "configured", "pier.yaml")
	writeFile(t, configPath, "name: custom\n")

	context, err := Find(configPath)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if context.Root != filepath.Dir(configPath) {
		t.Errorf("Find() root = %q, want %q", context.Root, filepath.Dir(configPath))
	}
	if context.ConfigPath != configPath {
		t.Errorf("Find() config path = %q, want explicit path %q", context.ConfigPath, configPath)
	}
}

func TestFindCreatesMissingProjectIDForHandAuthoredConfig(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pier.yaml"), "version: 1\nname: hand-authored\nservices: {}\n")
	writeFile(t, filepath.Join(root, ".gitignore"), "dist/\n")

	context, err := Find(root)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(context.ID) {
		t.Errorf("Find() ID = %q, want UUID-shaped identifier", context.ID)
	}

	idPath := filepath.Join(root, ".pier", "id")
	contents, err := os.ReadFile(idPath)
	if err != nil {
		t.Fatalf("read generated project ID: %v", err)
	}
	if string(contents) != context.ID+"\n" {
		t.Errorf("generated project ID = %q, want returned ID followed by newline", contents)
	}
	info, err := os.Stat(idPath)
	if err != nil {
		t.Fatalf("stat generated project ID: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("generated project ID permissions = %#o, want 0600", info.Mode().Perm())
	}
	gitignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if string(gitignore) != "dist/\n.pier/\n" {
		t.Errorf(".gitignore = %q, want existing entries plus .pier/", gitignore)
	}
}

func TestFindRejectsIncompleteProjectID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pier.yaml"), "version: 1\nname: hand-authored\nservices: {}\n")
	writeFile(t, filepath.Join(root, ".pier", "id"), "\n")

	context, err := Find(root)
	if err == nil {
		t.Fatal("Find() error = nil, want incomplete project ID error")
	}
	if context != (Context{}) {
		t.Errorf("Find() context = %#v, want empty context", context)
	}
}

func TestFindReturnsInitInstructionWhenNoConfigExists(t *testing.T) {
	context, err := Find(t.TempDir())
	if context != (Context{}) {
		t.Errorf("Find() context = %#v, want empty context", context)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Find() error = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "run pier init") {
		t.Errorf("Find() error = %q, want instruction to run pier init", err)
	}
}

func TestInitRefusesExistingConfig(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "pier.yaml")
	const original = "version: 1\nname: existing\nservices: {}\n"
	writeFile(t, configPath, original)

	if _, err := Init(root, "new-project"); err == nil {
		t.Fatal("Init() error = nil, want refusal to overwrite existing pier.yaml")
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read existing config: %v", err)
	}
	if string(contents) != original {
		t.Errorf("Init() replaced existing config with %q, want it unchanged", contents)
	}
}

func TestInitCreatesMinimalProjectFiles(t *testing.T) {
	root := t.TempDir()

	context, err := Init(root, "demo")
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if context.Root != root || context.ConfigPath != filepath.Join(root, "pier.yaml") {
		t.Errorf("Init() context = %#v, want root and pier.yaml path", context)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(context.ID) {
		t.Errorf("Init() ID = %q, want UUID-shaped identifier", context.ID)
	}

	raw, err := config.Load(context.ConfigPath)
	if err != nil {
		t.Fatalf("load initialized config: %v", err)
	}
	if raw.Services["web"].Public == nil || *raw.Services["web"].Public {
		t.Errorf("Init() web public = %v, want explicit false boolean", raw.Services["web"].Public)
	}
	project, err := config.Normalize(raw)
	if err != nil {
		t.Fatalf("normalize initialized config: %v", err)
	}
	if project.Name != "demo" || len(project.Services) != 1 || project.Services[0].Name != "web" || project.Services[0].Target != "http://127.0.0.1:3000" || project.Services[0].Path != "/" || project.Services[0].Public {
		t.Errorf("Init() project = %#v, want the minimal private web configuration", project)
	}

	idContents, err := os.ReadFile(filepath.Join(root, ".pier", "id"))
	if err != nil {
		t.Fatalf("read project ID: %v", err)
	}
	if string(idContents) != context.ID+"\n" {
		t.Errorf("project ID file = %q, want returned ID followed by newline", idContents)
	}
	info, err := os.Stat(filepath.Join(root, ".pier", "id"))
	if err != nil {
		t.Fatalf("stat project ID: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("project ID permissions = %#o, want 0600", info.Mode().Perm())
	}
}

func TestInitAppendsPierIgnoreWithoutReplacingExistingEntries(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".gitignore"), "dist/\ncoverage.out\n")

	if _, err := Init(root, "demo"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if string(contents) != "dist/\ncoverage.out\n.pier/\n" {
		t.Errorf(".gitignore = %q, want existing entries plus .pier/", contents)
	}
}

func TestInitDoesNotDuplicatePierIgnoreEntry(t *testing.T) {
	root := t.TempDir()
	const original = ".pier/\ndist/\n"
	writeFile(t, filepath.Join(root, ".gitignore"), original)

	if _, err := Init(root, "demo"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if string(contents) != original {
		t.Errorf(".gitignore = %q, want existing .pier/ entry unchanged", contents)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
