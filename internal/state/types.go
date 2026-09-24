package state

import (
	"errors"
	"strconv"
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
	Taps       []Tap           `json:"taps,omitempty"`
	// PublicUntil is when each timed `public:` window closes.
	PublicUntil map[string]time.Time `json:"publicUntil,omitempty"`
	UpdatedAt   time.Time            `json:"updatedAt"`
}

// ExpiryDue reports whether a public window has closed while this project
// still owns the service's public route.
func (p ProjectState) ExpiryDue(now time.Time) bool {
	for _, route := range p.Routes {
		if end, ok := p.PublicUntil[route.Service]; ok && route.HTTPSPort == 443 && !now.Before(end) {
			return true
		}
	}
	return false
}

// OwnsTimedPublic reports whether the project holds a public route with a
// window, so Pier's background process must stay up to close it.
func (p ProjectState) OwnsTimedPublic() bool {
	for _, route := range p.Routes {
		if _, ok := p.PublicUntil[route.Service]; ok && route.HTTPSPort == 443 {
			return true
		}
	}
	return false
}

// Tap is a loopback port where the daemon takes a service's traffic before
// the service does, to throttle or capture it. Tailscale routes point at the
// tap; the service's .local name uses the same throttle and capture.
type Tap struct {
	Service string `json:"service"`
	// Target is the service's real loopback URL.
	Target string `json:"target"`
	Port   int    `json:"port"`
	// Throttle is nil at full speed.
	Throttle *Throttle `json:"throttle,omitempty"`
	// CaptureSeconds is how long captured requests are kept; 0 captures nothing.
	CaptureSeconds int64 `json:"captureSeconds,omitempty"`
}

// Throttle is a saved throttle: latency, and bytes per second each way (0 is unlimited).
type Throttle struct {
	LatencyMS int64 `json:"latencyMs,omitempty"`
	Down      int64 `json:"down,omitempty"`
	Up        int64 `json:"up,omitempty"`
}

// Address is where the tap listens.
func (t Tap) Address() string { return "127.0.0.1:" + strconv.Itoa(t.Port) }

// URL is what Tailscale routes point at in place of the service.
func (t Tap) URL() string { return "http://" + t.Address() }

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
