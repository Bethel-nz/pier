package state

import (
	"errors"
	"time"
)

// CurrentVersion is the persisted project-state schema version.
const CurrentVersion = 1

// ErrUnsupportedVersion identifies a state file Pier cannot load or migrate.
var ErrUnsupportedVersion = errors.New("unsupported state version")

// ProjectState is the persisted ownership and runtime-override record for one project.
type ProjectState struct {
	Version    int             `json:"version"`
	ProjectID  string          `json:"projectId"`
	Name       string          `json:"name,omitempty"`
	Path       string          `json:"path,omitempty"`
	DNSName    string          `json:"dnsName,omitempty"`
	ConfigHash string          `json:"configHash,omitempty"`
	Routes     []Route         `json:"routes,omitempty"`
	Overrides  map[string]bool `json:"overrides,omitempty"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}

// Route identifies a Tailscale route owned by a Pier project.
type Route struct {
	Service   string `json:"service"`
	HTTPSPort uint16 `json:"httpsPort"`
	Path      string `json:"path"`
}
