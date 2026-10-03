package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Bethel-nz/pier/internal/localname"
	"github.com/Bethel-nz/pier/internal/localproxy"
	"github.com/Bethel-nz/pier/internal/reconcile"
	"github.com/Bethel-nz/pier/internal/state"
)

// probeTimeout bounds one probe of a public or tailnet URL.
const probeTimeout = 10 * time.Second

// Supervise is the background check of a project's exposure paths: it puts
// back the Tailscale routes the project owns when they went missing or
// changed, then probes every URL end to end. Pier's background process runs
// it every 30 seconds. It never touches a route the project does not own,
// never deletes one, and changes no saved state.
func (s *Service) Supervise(ctx context.Context, start string) (localname.SupervisePass, error) {
	var pass localname.SupervisePass
	loaded, err := s.loadProject(start, true)
	if err != nil {
		return pass, err
	}
	st, err := s.store.Load(loaded.project.ID)
	if err != nil {
		return pass, fmt.Errorf("Pier could not load project state: %w", err)
	}
	var tsErr error
	if len(st.Routes) > 0 {
		tsErr = s.superviseTailscale(ctx, loaded, st, &pass)
	}
	if st.Tunnel.Serving() {
		for _, host := range st.Tunnel.Hosts {
			pass.Probes = append(pass.Probes, s.probe(ctx, host.Service, "https://"+host.Hostname+"/"))
		}
	}
	return pass, tsErr
}

func (s *Service) superviseTailscale(ctx context.Context, loaded loadedProject, st state.ProjectState, pass *localname.SupervisePass) error {
	if _, err := s.ts.Check(ctx); err != nil {
		return &PrerequisiteError{Err: err}
	}
	actual, err := s.ts.Routes(ctx)
	if err != nil {
		return fmt.Errorf("Pier could not read Tailscale routes: %w", err)
	}
	if hash := configHash(loaded.project.ConfigPath); hash != "" && st.ConfigHash != "" && hash != st.ConfigHash {
		// The owned routes came from the old pier.yaml; putting them back
		// from the new one would apply an edit nobody asked to apply yet.
		pass.Note = "pier.yaml changed since pier up, so Pier is not putting routes back; run pier up"
	} else if repaired, err := s.repairRoutes(ctx, loaded, st, actual); err != nil {
		return err
	} else if len(repaired) > 0 {
		pass.Repaired = repaired
	}
	dns := s.lookupDNS(ctx, st.DNSName)
	for _, route := range st.Routes {
		if route.TCP {
			continue // no HTTP to probe; the route check above covers it
		}
		if url := routeURL(dns, ownedRoute(route)); url != "" {
			pass.Probes = append(pass.Probes, s.probe(ctx, route.Service, url))
		}
	}
	return nil
}

// repairRoutes plans pier up's routes, keeps only creates and updates of
// routes the project already owns, and applies them. It returns the
// services whose route it put back.
func (s *Service) repairRoutes(ctx context.Context, loaded loadedProject, st state.ProjectState, actual []reconcile.Route) ([]string, error) {
	cfg := withExpiry(loaded.cfg, st.PublicUntil, s.clock())
	owned := ownedRoutes(loaded.project, st)
	mine := make(map[string]bool, len(owned))
	for _, route := range owned {
		mine[route.Key()] = true
	}
	var desired []reconcile.Route
	for _, route := range desiredRoutes(loaded.project, cfg, st.Overrides, st.Paused, st.Taps) {
		if mine[route.Key()] {
			desired = append(desired, route)
		}
	}
	plan := s.planner(desired, actual, owned, false)
	var repair reconcile.Plan
	for _, op := range plan.Operations {
		if (op.Kind == reconcile.KindCreate || op.Kind == reconcile.KindUpdate) && mine[op.After.Key()] {
			repair.Operations = append(repair.Operations, op)
		}
	}
	if len(repair.Operations) == 0 {
		return nil, nil
	}
	result, err := reconcile.Apply(ctx, repair, s.ts)
	var repaired []string
	for _, op := range result.Completed {
		repaired = append(repaired, op.After.Service)
	}
	if err != nil {
		return repaired, &ApplyError{Err: err, Result: result}
	}
	return repaired, nil
}

// probe asks url for its headers the way a visitor would. Any answer below
// 500 means the path works end to end; the app decides what it says.
func (s *Service) probe(ctx context.Context, service, url string) localname.Probe {
	result := localname.Probe{Service: service, URL: url}
	do := s.fetch
	if do == nil {
		do = probeClient.Do
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	req.Header.Set("User-Agent", "pier-probe")
	req.Header.Set(localproxy.ProbeHeader, "1")
	start := s.clock()
	resp, err := do(req)
	result.LatencyMS = s.clock().Sub(start).Milliseconds()
	if err != nil {
		result.Error = probeError(err)
		return result
	}
	_ = resp.Body.Close()
	result.Status = resp.StatusCode
	if resp.StatusCode >= 500 {
		result.Error = fmt.Sprintf("answered %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return result
}

var probeClient = &http.Client{
	// A redirect is an answer; following it would probe somewhere else.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func probeError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "no answer within 10s"
	}
	return err.Error()
}

// withSupervision adds the background check's last result to each service.
func withSupervision(infos []ServiceInfo, record localname.Supervision) {
	for i := range infos {
		infos[i].RepairedAt = record.Repaired[infos[i].Name]
		for _, probe := range record.Probes {
			if probe.Service != infos[i].Name {
				continue
			}
			infos[i].VerifiedAt = probe.LastSuccess
			infos[i].VerifyError = probe.Error
			infos[i].FailingSince = probe.FailingSince
		}
	}
}

// supervisionNotes says why the background check could not do its job.
func supervisionNotes(record localname.Supervision) []string {
	var notes []string
	if record.Error != "" {
		notes = append(notes, "Pier's background check has failed since "+record.ErrorSince.Format("15:04")+" ("+record.Error+"); next try at "+record.NextCheck.Format("15:04:05"))
	}
	if record.Note != "" {
		notes = append(notes, record.Note)
	}
	return notes
}
