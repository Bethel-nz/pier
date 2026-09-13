package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Store persists per-project ownership and runtime overrides as JSON files.
type Store struct {
	dir string
}

// Open returns a store under the operating-system user config directory.
func Open() (*Store, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user config directory: %w", err)
	}
	return New(filepath.Join(base, "pier", "projects")), nil
}

// New returns a store rooted at dir. Each project is stored as <project-id>.json.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// Load reads project state. Missing state returns an empty versioned value.
func (s *Store) Load(projectID string) (ProjectState, error) {
	path := s.pathFor(projectID)
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ProjectState{Version: CurrentVersion}, nil
		}
		return ProjectState{}, fmt.Errorf("read project state: %w", err)
	}

	var state ProjectState
	if err := json.Unmarshal(contents, &state); err != nil {
		return ProjectState{}, fmt.Errorf("decode project state: %w", err)
	}
	if state.Version != CurrentVersion {
		return ProjectState{}, fmt.Errorf("%w: state version %d in %s cannot be loaded; migrate or remove the file", ErrUnsupportedVersion, state.Version, path)
	}
	return state, nil
}

// Save atomically writes project state.
func (s *Store) Save(state ProjectState) error {
	if state.ProjectID == "" {
		return fmt.Errorf("save project state: project ID is required")
	}
	if state.Version == 0 {
		state.Version = CurrentVersion
	}
	if state.Version != CurrentVersion {
		return fmt.Errorf("%w: refusing to write state version %d", ErrUnsupportedVersion, state.Version)
	}

	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	contents, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project state: %w", err)
	}
	contents = append(contents, '\n')

	temporary, err := os.CreateTemp(s.dir, "."+fileID(state.ProjectID)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary project state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("set temporary project state permissions: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary project state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary project state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary project state: %w", err)
	}

	path := s.pathFor(state.ProjectID)
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish project state: %w", err)
	}
	return nil
}

// Delete removes persisted state for projectID. Missing state is not an error.
func (s *Store) Delete(projectID string) error {
	err := os.Remove(s.pathFor(projectID))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete project state: %w", err)
	}
	return nil
}

func (s *Store) pathFor(projectID string) string {
	return filepath.Join(s.dir, fileID(projectID)+".json")
}

func fileID(projectID string) string {
	id := filepath.Base(filepath.Clean(projectID))
	if id == "." || id == ".." || id == "" {
		return "project"
	}
	return id
}
