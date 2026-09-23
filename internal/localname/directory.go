package localname

import (
	"context"
	"fmt"

	"github.com/Bethel-nz/pier/internal/state"
)

// Directory keeps the machine's published names matched to saved project state.
type Directory struct {
	projects *state.Store
}

// NewDirectory publishes names recorded in projects.
func NewDirectory(projects *state.Store) *Directory {
	return &Directory{projects: projects}
}

// Sync starts publishing when any project has a local domain, and withdraws
// every name when none remain.
func (d *Directory) Sync(context.Context) error {
	if d == nil || d.projects == nil {
		return nil
	}
	projects, err := d.projects.List()
	if err != nil {
		return fmt.Errorf("Pier could not read local domains: %w", err)
	}
	records, err := Records(projects)
	if err != nil {
		return fmt.Errorf("Pier could not publish local domains: %w", err)
	}
	if len(records) == 0 {
		if err := stopDaemon(); err != nil {
			return err
		}
		return Release()
	}
	return ensureDaemon()
}
