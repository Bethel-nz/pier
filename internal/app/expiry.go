package app

import (
	"context"
	"errors"
	"time"

	"github.com/Bethel-nz/pier/internal/config"
	"github.com/Bethel-nz/pier/internal/reconcile"
	"github.com/Bethel-nz/pier/internal/state"
)

// renewWindows opens a full public window for every service with a timed
// `public:`. Each pier up renews it: public for 2h means 2h from the last up.
func renewWindows(cfg config.Project, now time.Time) map[string]time.Time {
	var until map[string]time.Time
	for _, service := range cfg.Services {
		if service.Public && service.PublicFor > 0 {
			if until == nil {
				until = map[string]time.Time{}
			}
			until[service.Name] = now.Add(service.PublicFor).Truncate(time.Second)
		}
	}
	return until
}

// withExpiry makes each timed service private once its window has closed,
// or before one was ever opened. pier share still overrides it.
func withExpiry(cfg config.Project, until map[string]time.Time, now time.Time) config.Project {
	services := append([]config.ResolvedService(nil), cfg.Services...)
	for i, service := range services {
		if !service.Public || service.PublicFor == 0 {
			continue
		}
		if end, ok := until[service.Name]; !ok || !now.Before(end) {
			services[i].Public = false
			services[i].HTTPSPort = 8443
		}
	}
	cfg.Services = services
	return cfg
}

// withPublicUntil shows when each open window closes, unless pier share or
// unshare overrode the service.
func withPublicUntil(infos []ServiceInfo, until map[string]time.Time, overrides map[string]bool, now time.Time) {
	for i := range infos {
		if _, overridden := overrides[infos[i].Name]; overridden {
			continue
		}
		if end, ok := until[infos[i].Name]; ok && now.Before(end) && infos[i].Public {
			infos[i].PublicUntil = end
		}
	}
}

// untimedPublic suggests a time limit when this run makes a service public
// without one. It speaks once, as the route is created, not on every up.
func untimedPublic(plan reconcile.Plan, cfg config.Project, overrides map[string]bool) []string {
	timed := map[string]bool{}
	for _, service := range cfg.Services {
		timed[service.Name] = service.PublicFor > 0
	}
	var hints []string
	for _, op := range plan.Operations {
		if op.Kind != reconcile.KindCreate && op.Kind != reconcile.KindUpdate || !op.After.Public {
			continue
		}
		if _, shared := overrides[op.After.Service]; shared || timed[op.After.Service] {
			continue
		}
		hints = append(hints, op.After.Service+" is now PUBLIC with no time limit; public: 2h in pier.yaml would close it on its own")
	}
	return hints
}

// needsDaemon reports whether Pier's background process has work for the
// project besides its .local names: taps, or a public window to close.
func needsDaemon(st state.ProjectState) bool {
	return len(st.Taps) > 0 || st.OwnsTimedPublic()
}

func sameTimes(left, right map[string]time.Time) bool {
	if len(left) != len(right) {
		return false
	}
	for name, at := range left {
		if other, ok := right[name]; !ok || !other.Equal(at) {
			return false
		}
	}
	return true
}

// Expire withdraws public access whose window has closed, without opening a
// new one. Pier's background process runs it when a window ends.
func (s *Service) Expire(ctx context.Context, start string) error {
	result, err := s.reconcile(ctx, start, false, false, false, nil, "")
	if err == nil && result.TailscaleSkipped != "" {
		return errors.New(result.TailscaleSkipped)
	}
	return err
}
