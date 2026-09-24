package reconcile

import "fmt"

// Kind is a planned route operation.
type Kind string

const (
	KindKeep   Kind = "keep"
	KindCreate Kind = "create"
	KindUpdate Kind = "update"
	KindDelete Kind = "delete"
)

// Route is one desired, actual, or owned Tailscale route: an HTTPS path, or
// a TCP port when TCP is set.
type Route struct {
	Service   string
	ProjectID string
	// HTTPSPort is the listener: the HTTPS port, or the TCP port.
	HTTPSPort uint16
	Path      string
	Target    string
	Public    bool
	TCP       bool
}

// Key returns the external Tailscale identity: HTTPS listener plus normalized
// path, or the TCP port, which a TCP forward owns whole.
func (r Route) Key() string {
	if r.TCP {
		return fmt.Sprintf("tcp:%d", r.HTTPSPort)
	}
	return fmt.Sprintf("https:%d:%s", r.HTTPSPort, r.Path)
}

// Operation is one keep, create, update, or delete in a plan.
type Operation struct {
	Kind   Kind
	Before Route
	After  Route
}

// Conflict is an unmanaged actual route that does not match the desired route.
type Conflict struct {
	Desired  Route
	Actual   Route
	Takeover bool
}

// Plan is the side-effect-free reconciliation result.
type Plan struct {
	Operations []Operation
	Conflicts  []Conflict
}
