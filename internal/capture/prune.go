package capture

import (
	"context"
	"time"
)

// PruneEvery is how often the pruning routine runs.
const PruneEvery = time.Minute

// Prunes runs as its own routine until ctx ends. Right away and then every
// interval, it deletes each service's requests older than keep() allows, and
// every request of a service keep() no longer lists.
func (s *Store) Prunes(ctx context.Context, interval time.Duration, keep func() map[string]time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		s.pruneOnce(ctx, keep(), time.Now())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Store) pruneOnce(ctx context.Context, keep map[string]time.Duration, now time.Time) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	services := make([]string, 0, len(keep))
	for service, span := range keep {
		services = append(services, service)
		_, _ = s.Prune(ctx, service, now.Add(-span))
	}
	_ = s.PruneExcept(ctx, services)
}
