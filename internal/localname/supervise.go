package localname

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/Bethel-nz/pier/internal/state"
)

const (
	// superviseEvery is how often the daemon re-checks each project's routes.
	superviseEvery = 30 * time.Second
	// superviseMaxBackoff caps the wait between checks while Tailscale is down.
	superviseMaxBackoff = 5 * time.Minute
	// superviseTimeout bounds one check, probes included.
	superviseTimeout = 25 * time.Second
	// busyRetry is how soon to check again after a command held the project.
	busyRetry = 5 * time.Second
)

// SupervisePass is one look at a project's exposure paths, from the
// Supervise hook: routes put back, and how each public URL answered.
type SupervisePass struct {
	// Repaired names the services whose missing or changed route was put back.
	Repaired []string
	Probes   []Probe
	// Note says why routes were not repaired, such as pier.yaml having
	// changed since pier up. It is not an error and does not back off.
	Note string
	// Busy is set when a command was changing the project's routes, so
	// nothing was checked; the daemon tries again in a few seconds.
	Busy bool
}

// Probe is one request to a URL Pier made public or tailnet-reachable.
type Probe struct {
	Service string `json:"service"`
	URL     string `json:"url"`
	// Status is the HTTP status, 0 when the request failed.
	Status    int       `json:"status,omitempty"`
	LatencyMS int64     `json:"latencyMs,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
	// Error says why the URL counts as failing: no answer, or a 5xx.
	Error        string    `json:"error,omitempty"`
	LastSuccess  time.Time `json:"lastSuccess,omitempty"`
	FailingSince time.Time `json:"failingSince,omitempty"`
}

// Supervision is the daemon's running record for one project.
type Supervision struct {
	Project   string    `json:"project"`
	CheckedAt time.Time `json:"checkedAt,omitempty"`
	// Error is why the last check could not finish, such as Tailscale being down.
	Error      string    `json:"error,omitempty"`
	ErrorSince time.Time `json:"errorSince,omitempty"`
	NextCheck  time.Time `json:"nextCheck,omitempty"`
	Note       string    `json:"note,omitempty"`
	// Repairs counts routes put back since the daemon started.
	Repairs int `json:"repairs,omitempty"`
	// Repaired is when each service's route was last put back.
	Repaired map[string]time.Time `json:"repaired,omitempty"`
	Probes   []Probe              `json:"probes,omitempty"`
}

// watch schedules one project's checks.
type watch struct {
	mu       sync.Mutex
	running  bool
	failures int
	record   Supervision
}

// supervised reports whether the daemon watches project: it owns Tailscale
// routes or serves a Cloudflare Tunnel.
func supervised(project state.ProjectState) bool {
	return project.Path != "" && (len(project.Routes) > 0 || project.Tunnel.Serving())
}

// superviseAll starts each due check and returns every project's record,
// plus a warning for each one that is failing.
func (d *daemon) superviseAll(saved []state.ProjectState, now time.Time) ([]Supervision, []string, bool) {
	active := false
	seen := map[string]bool{}
	var records []Supervision
	var warnings []string
	for _, project := range saved {
		if !supervised(project) {
			continue
		}
		active = true
		seen[project.ProjectID] = true
		if d.supervise == nil {
			continue
		}
		w := d.watches[project.ProjectID]
		if w == nil {
			w = &watch{record: Supervision{Project: project.ProjectID}}
			d.watches[project.ProjectID] = w
		}
		w.mu.Lock()
		if !w.running && !now.Before(w.record.NextCheck) && !d.expiryRunning(project.ProjectID) {
			w.running = true
			go d.check(w, project.Path)
		}
		records = append(records, w.record)
		warnings = append(warnings, superviseWarnings(project.Name, w.record)...)
		w.mu.Unlock()
	}
	for id := range d.watches {
		if !seen[id] {
			delete(d.watches, id)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Project < records[j].Project })
	return records, warnings, active
}

// expiryRunning keeps a check from racing a window being closed.
func (d *daemon) expiryRunning(projectID string) bool {
	e := d.expiring[projectID]
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

func (d *daemon) check(w *watch, root string) {
	ctx, cancel := context.WithTimeout(d.ctx, superviseTimeout)
	pass, err := d.supervise(ctx, root)
	cancel()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.running = false
	if d.ctx.Err() != nil {
		return
	}
	w.failures = w.record.merge(pass, err, time.Now(), w.failures)
}

// merge folds one pass into the record, keeping each URL's history, and
// returns the new failure count. A failing check backs off, doubling up to
// superviseMaxBackoff, so a stopped Tailscale is asked rarely, not hammered.
func (s *Supervision) merge(pass SupervisePass, err error, now time.Time, failures int) int {
	if pass.Busy && err == nil {
		s.NextCheck = now.Add(busyRetry)
		return failures
	}
	s.CheckedAt = now
	s.Note = pass.Note
	if err != nil {
		if s.Error == "" {
			s.ErrorSince = now
		}
		s.Error = err.Error()
		failures++
	} else {
		s.Error, s.ErrorSince = "", time.Time{}
		failures = 0
	}
	s.NextCheck = now.Add(backoff(failures))
	for _, service := range pass.Repaired {
		if s.Repaired == nil {
			s.Repaired = map[string]time.Time{}
		}
		s.Repaired[service] = now
		s.Repairs++
	}
	if len(pass.Probes) == 0 && err != nil {
		return failures // nothing was probed; keep what is known
	}
	previous := map[string]Probe{}
	for _, probe := range s.Probes {
		previous[probe.URL] = probe
	}
	probes := make([]Probe, 0, len(pass.Probes))
	for _, probe := range pass.Probes {
		before := previous[probe.URL]
		probe.CheckedAt = now
		if probe.Error == "" {
			probe.LastSuccess = now
		} else {
			probe.LastSuccess, probe.FailingSince = before.LastSuccess, before.FailingSince
			if probe.FailingSince.IsZero() {
				probe.FailingSince = now
			}
		}
		probes = append(probes, probe)
	}
	s.Probes = probes
	return failures
}

func backoff(failures int) time.Duration {
	wait := superviseEvery
	for i := 0; i < failures && wait < superviseMaxBackoff; i++ {
		wait *= 2
	}
	return min(wait, superviseMaxBackoff)
}

// superviseWarnings says once per state what is wrong, not once per check.
func superviseWarnings(project string, record Supervision) []string {
	var out []string
	if record.Error != "" {
		out = append(out, "Pier cannot check "+project+"'s routes ("+record.Error+"); it will try again "+record.NextCheck.Format("15:04:05"))
	}
	for _, probe := range record.Probes {
		if probe.Error != "" {
			out = append(out, probe.URL+" failing since "+probe.FailingSince.Format("15:04")+": "+probe.Error)
		}
	}
	return out
}
