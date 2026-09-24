package localname

import (
	"sync"
	"time"

	"github.com/Bethel-nz/pier/internal/state"
)

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// expireRetry spaces attempts to close a window, for when Tailscale is down.
const expireRetry = 30 * time.Second

// expiry tracks closing one project's public windows.
type expiry struct {
	mu      sync.Mutex
	running bool
	tried   time.Time
	err     error
}

// closeWindows starts closing every public window that has ended. It reports
// whether some project still holds a timed public route, which keeps the
// daemon running until it closes.
func (d *daemon) closeWindows(saved []state.ProjectState, now time.Time) (bool, []string) {
	pending := false
	var warnings []string
	for _, project := range saved {
		if project.Path == "" || !project.OwnsTimedPublic() {
			delete(d.expiring, project.ProjectID)
			continue
		}
		pending = true
		if d.expire == nil || !project.ExpiryDue(now) {
			continue
		}
		e := d.expiring[project.ProjectID]
		if e == nil {
			e = &expiry{}
			d.expiring[project.ProjectID] = e
		}
		e.mu.Lock()
		if e.err != nil {
			warnings = append(warnings, "Pier could not end public access for "+project.Name+": "+e.err.Error())
		}
		if !e.running && now.Sub(e.tried) >= expireRetry {
			e.running, e.tried = true, now
			go func(root string) {
				err := d.expire(d.ctx, root)
				e.mu.Lock()
				e.running, e.err = false, err
				e.mu.Unlock()
			}(project.Path)
		}
		e.mu.Unlock()
	}
	return pending, warnings
}
