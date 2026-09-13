// Package project discovers and initializes Pier project directories.
package project

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound indicates that no Pier configuration could be found.
var ErrNotFound = errors.New("Pier project not found")

// Context identifies a Pier project's files.
type Context struct {
	Root       string
	ConfigPath string
	ID         string
}

// Find resolves start as either a directory to search upward from or an
// explicit pier.yaml path.
func Find(start string) (Context, error) {
	start, err := filepath.Abs(filepath.Clean(start))
	if err != nil {
		return Context{}, fmt.Errorf("resolve project path: %w", err)
	}

	info, err := os.Stat(start)
	if err != nil {
		return Context{}, fmt.Errorf("inspect project path: %w", err)
	}
	if !info.IsDir() {
		return contextForConfig(start)
	}

	for directory := start; ; directory = filepath.Dir(directory) {
		configPath := filepath.Join(directory, "pier.yaml")
		if _, err := os.Stat(configPath); err == nil {
			return contextForConfig(configPath)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return Context{}, fmt.Errorf("inspect project configuration: %w", err)
		}

		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
	}

	return Context{}, fmt.Errorf("%w: no pier.yaml found; run pier init", ErrNotFound)
}

// Init creates a minimal Pier project without replacing an existing config.
func Init(dir, name string) (Context, error) {
	root, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return Context{}, fmt.Errorf("resolve project directory: %w", err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Context{}, fmt.Errorf("create project directory: %w", err)
	}

	configPath := filepath.Join(root, "pier.yaml")
	if _, err := os.Stat(configPath); err == nil {
		return Context{}, fmt.Errorf("refuse to overwrite existing %s", configPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Context{}, fmt.Errorf("inspect project configuration: %w", err)
	}

	id, err := ensureProjectID(root)
	if err != nil {
		return Context{}, err
	}

	contents := fmt.Sprintf("version: 1\nname: %s\n\nservices:\n  web:\n    target: localhost:3000\n    public: false\n", name)
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		return Context{}, fmt.Errorf("write project configuration: %w", err)
	}
	if err := ensureGitignore(root); err != nil {
		return Context{}, err
	}

	return Context{Root: root, ConfigPath: configPath, ID: id}, nil
}

func contextForConfig(configPath string) (Context, error) {
	configPath, err := filepath.Abs(filepath.Clean(configPath))
	if err != nil {
		return Context{}, fmt.Errorf("resolve configuration path: %w", err)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		return Context{}, fmt.Errorf("inspect project configuration: %w", err)
	}
	if info.IsDir() {
		return Context{}, fmt.Errorf("project configuration is a directory: %s", configPath)
	}

	root := filepath.Dir(configPath)
	id, err := ensureProjectID(root)
	if err != nil {
		return Context{}, err
	}
	return Context{Root: root, ConfigPath: configPath, ID: id}, nil
}

func ensureProjectID(root string) (string, error) {
	path := filepath.Join(root, ".pier", "id")
	contents, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(contents)), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read project ID: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create project state directory: %w", err)
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("read concurrently created project ID: %w", readErr)
		}
		return strings.TrimSpace(string(contents)), nil
	}
	if err != nil {
		return "", fmt.Errorf("create project ID: %w", err)
	}
	defer file.Close()
	if _, err := file.WriteString(id + "\n"); err != nil {
		return "", fmt.Errorf("write project ID: %w", err)
	}
	return id, nil
}

func ensureGitignore(root string) error {
	path := filepath.Join(root, ".gitignore")
	contents, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	for _, entry := range strings.Split(string(contents), "\n") {
		if entry == ".pier/" {
			return nil
		}
	}

	addition := ".pier/\n"
	if len(contents) > 0 && !strings.HasSuffix(string(contents), "\n") {
		addition = "\n" + addition
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open .gitignore: %w", err)
	}
	defer file.Close()
	if _, err := file.WriteString(addition); err != nil {
		return fmt.Errorf("append .pier/ to .gitignore: %w", err)
	}
	return nil
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate project ID: %w", err)
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16]), nil
}
