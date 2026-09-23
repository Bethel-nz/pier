// Package health probes local service targets for availability.
package health

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/Bethel-nz/pier/internal/config"
)

// DefaultTimeout is the per-service dial deadline.
const DefaultTimeout = 500 * time.Millisecond

// Status is a local target availability label.
type Status string

const (
	StatusHealthy     Status = "healthy"
	StatusUnavailable Status = "unavailable"
)

// Result is the outcome of one bounded target check.
type Result struct {
	Service string
	Status  Status
	Error   string
}

// Check dials the normalized host and port. Failures are returned as data.
func Check(ctx context.Context, service config.ResolvedService) Result {
	result := Result{Service: service.Name, Status: StatusUnavailable}
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	addr := net.JoinHostPort(service.Host, strconv.FormatUint(uint64(service.Port), 10))
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	_ = conn.Close()
	result.Status = StatusHealthy
	return result
}
