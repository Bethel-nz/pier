package config

import (
	"fmt"
	"net"
	"strings"
)

// TunnelName is the Cloudflare Tunnel that serves the project's cloudflare:
// hostnames. The pier- prefix keeps it apart from tunnels made by hand.
func (p Project) TunnelName() string {
	var name strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(p.Name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			name.WriteRune(r)
		default:
			name.WriteRune('-')
		}
	}
	return "pier-" + strings.Trim(name.String(), "-")
}

// HasCloudflare reports whether any service is served through Cloudflare.
func (p Project) HasCloudflare() bool {
	for _, service := range p.Services {
		if service.Cloudflare != "" {
			return true
		}
	}
	return false
}

// cloudflareErrors checks a cloudflare: hostname: a real DNS name, one per
// service, on an HTTP service.
func cloudflareErrors(service ResolvedService, claimed map[string]string) []ValidationError {
	host := service.Cloudflare
	if host == "" {
		return nil
	}
	fail := func(message string) []ValidationError {
		return []ValidationError{serviceError(service.Name, "cloudflare", message)}
	}
	switch {
	case service.TCP():
		return fail("works on HTTP services only; Cloudflare TCP needs cloudflared on every client")
	case strings.Contains(host, "://") || strings.Contains(host, "/") || net.ParseIP(host) != nil:
		return fail("must be a hostname, such as app.example.com, not a URL or address")
	case strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".ts.net"):
		return fail("must be a hostname on a Cloudflare zone you own, such as app.example.com")
	case !strings.Contains(host, "."):
		return fail("must include the domain, such as app.example.com")
	case len(host) > 253:
		return fail("must be 253 characters or fewer")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) > 63 || !domainLabel.MatchString(label) {
			return fail("must use lowercase letters, digits, and hyphens, such as app.example.com")
		}
	}
	if owner := claimed[host]; owner != "" {
		return fail(fmt.Sprintf("duplicates the cloudflare hostname of service %q", owner))
	}
	claimed[host] = service.Name
	// One way in: Cloudflare or Tailscale, never both for the same service.
	var errs []ValidationError
	for field, set := range map[string]bool{"path": service.pathSet, "public": service.publicSet} {
		if set {
			errs = append(errs, serviceError(service.Name, field, "is a Tailscale setting; a service with cloudflare: is served by Cloudflare only, so remove "+field+" or cloudflare"))
		}
	}
	return errs
}
