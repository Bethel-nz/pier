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
	Domains    []LocalDomain   `json:"domains,omitempty"`
	Local      LocalSettings   `json:"local"`
	Overrides  map[string]bool `json:"overrides,omitempty"`
	Paused     map[string]bool `json:"paused,omitempty"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}

// Route identifies a Tailscale route owned by a Pier project.
type Route struct {
	Service   string `json:"service"`
	HTTPSPort uint16 `json:"httpsPort"`
	Path      string `json:"path"`
	// Public is set for a Funnel route: reachable from the internet.
	Public bool `json:"public,omitempty"`
	// Since is when Pier created the route or last changed it.
	Since time.Time `json:"since,omitempty"`
}

// LocalSettings is how the daemon serves a project's local names.
// The zero value is the default: on the LAN, Pier's certificate, no autostart.
type LocalSettings struct {
	// ThisMachineOnly refuses connections from other devices (local.lan: false).
	ThisMachineOnly bool `json:"thisMachineOnly,omitempty"`
	Autostart       bool `json:"autostart,omitempty"`
	// CertFile and KeyFile are absolute paths to the user's own certificate.
	CertFile string `json:"certFile,omitempty"`
	KeyFile  string `json:"keyFile,omitempty"`
}

// LocalDomain is a .local name Pier serves for one service.
// Target is the loopback URL the name routes to, such as http://127.0.0.1:3000.
type LocalDomain struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	Target  string `json:"target"`
}
