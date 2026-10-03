// Package health probes local service targets for availability.
package health

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Bethel-nz/pier/internal/config"
)

// DefaultTimeout is the per-service dial deadline.
const DefaultTimeout = 500 * time.Millisecond

// HTTPTimeout bounds a health: request, which does real work in the service.
const HTTPTimeout = 2 * time.Second

// Status is a local target availability label.
type Status string

const (
	StatusHealthy     Status = "healthy"
	StatusUnavailable Status = "unavailable"
	// StatusUnhealthy is a target that accepts connections but whose health
	// path fails, such as a dev server stuck on a compile error.
	StatusUnhealthy Status = "unhealthy"
)

// Result is the outcome of one bounded target check.
type Result struct {
	Service string
	Status  Status
	// Code is the health path's HTTP status, when Pier got one.
	Code  int
	Error string
}

// client checks health paths. Local dev servers use self-signed
// certificates, and a redirect already shows the service answers.
var client = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // loopback dev server
		DisableKeepAlives: true,
		Proxy:             nil,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Check dials the normalized host and port, then requests the health path
// when the service has one. Failures are returned as data.
func Check(ctx context.Context, service config.ResolvedService) Result {
	result := Result{Service: service.Name, Status: StatusUnavailable}
	dialCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	addr := net.JoinHostPort(service.Host, strconv.FormatUint(uint64(service.Port), 10))
	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	_ = conn.Close()
	result.Status = StatusHealthy
	if service.Health == "" || service.TCP() {
		return result
	}
	return checkPath(ctx, service, addr, result)
}

func checkPath(ctx context.Context, service config.ResolvedService, addr string, result Result) Result {
	ctx, cancel := context.WithTimeout(ctx, HTTPTimeout)
	defer cancel()
	scheme := "http"
	if service.Protocol == config.ProtocolHTTPS {
		scheme = "https"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://"+addr+service.Health, nil)
	if err != nil {
		result.Status, result.Error = StatusUnhealthy, err.Error()
		return result
	}
	resp, err := client.Do(req)
	if err != nil {
		result.Status, result.Error = StatusUnhealthy, err.Error()
		return result
	}
	_ = resp.Body.Close()
	result.Code = resp.StatusCode
	if resp.StatusCode >= 400 {
		result.Status = StatusUnhealthy
		result.Error = fmt.Sprintf("GET %s returned %d", service.Health, resp.StatusCode)
	}
	return result
}
