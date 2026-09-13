// Package project discovers and initializes Pier project directories.
package project

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrNotFound indicates that no Pier configuration could be found.
var ErrNotFound = errors.New("Pier project not found")

var projectIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

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

	id, _, err := ensureProjectID(root)
	if err != nil {
		return Context{}, err
	}

	contents, err := encodeInitConfig(name)
	if err != nil {
		return Context{}, err
	}
	if err := os.WriteFile(configPath, contents, 0o644); err != nil {
		return Context{}, fmt.Errorf("write project configuration: %w", err)
	}
	if err := ensureGitignore(root); err != nil {
		return Context{}, err
	}

	return Context{Root: root, ConfigPath: configPath, ID: id}, nil
}

func encodeInitConfig(name string) ([]byte, error) {
	public := false
	doc := struct {
		Version  int    `yaml:"version"`
		Name     string `yaml:"name"`
		Services map[string]struct {
			Target string `yaml:"target"`
			Public *bool  `yaml:"public"`
		} `yaml:"services"`
	}{
		Version: 1,
		Name:    name,
		Services: map[string]struct {
			Target string `yaml:"target"`
			Public *bool  `yaml:"public"`
		}{
			"web": {Target: "localhost:3000", Public: &public},
		},
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encode project configuration: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode project configuration: %w", err)
	}
	return buf.Bytes(), nil
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
	id, _, err := ensureProjectID(root)
	if err != nil {
		return Context{}, err
	}
	if err := ensureGitignore(root); err != nil {
		return Context{}, err
	}
	return Context{Root: root, ConfigPath: configPath, ID: id}, nil
}

func ensureProjectID(root string) (string, bool, error) {
	path := filepath.Join(root, ".pier", "id")
	contents, err := os.ReadFile(path)
	if err == nil {
		id, err := parseProjectID(path, contents)
		return id, false, err
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", false, fmt.Errorf("read project ID: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", false, fmt.Errorf("create project state directory: %w", err)
	}
	id, err := newID()
	if err != nil {
		return "", false, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".id-*")
	if err != nil {
		return "", false, fmt.Errorf("create temporary project ID: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return "", false, fmt.Errorf("set temporary project ID permissions: %w", err)
	}
	if _, err := temporary.WriteString(id + "\n"); err != nil {
		temporary.Close()
		return "", false, fmt.Errorf("write temporary project ID: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", false, fmt.Errorf("close temporary project ID: %w", err)
	}

	err = os.Link(temporaryPath, path)
	if errors.Is(err, fs.ErrExist) {
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", false, fmt.Errorf("read concurrently created project ID: %w", readErr)
		}
		concurrentID, parseErr := parseProjectID(path, contents)
		return concurrentID, false, parseErr
	}
	if err != nil {
		return "", false, fmt.Errorf("publish project ID: %w", err)
	}
	return id, true, nil
}

func parseProjectID(path string, contents []byte) (string, error) {
	id := strings.TrimSpace(string(contents))
	if !projectIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid project ID in %s", path)
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
