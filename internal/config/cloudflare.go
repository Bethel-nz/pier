package config

import (
	"fmt"
	"net"
	"strings"
)

// TunnelName is the Cloudflare Tunnel that serves the project's Cloudflare
// services. The pier- prefix keeps it apart from tunnels made by hand.
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
		if !service.OnTailscale() {
			return true
		}
	}
	return false
}

// publicHostname is where Cloudflare serves a service: hostname under
// domain, or the service's name when hostname is empty. A hostname already
// ending in domain is used as written.
func publicHostname(service, hostname, domain string) string {
	label := normalizeDomain(hostname)
	if label == "" {
		label = service
	}
	if domain == "" || label == domain || strings.HasSuffix(label, "."+domain) {
		return label
	}
	return label + "." + domain
}

// domainErrors checks the top-level domain: it must be a real domain, and
// Cloudflare services need one.
func domainErrors(project Project) []ValidationError {
	if project.Domain != "" {
		if message := invalidHostnameMessage(project.Domain); message != "" {
			return []ValidationError{{Field: "domain", Message: message}}
		}
		return nil
	}
	var errs []ValidationError
	for _, service := range project.Services {
		if !service.OnTailscale() {
			errs = append(errs, serviceError(service.Name, "provider", "cloudflare needs the domain to serve it under; add domain: example.com at the top of pier.yaml"))
		}
	}
	return errs
}

// providerErrors checks a service's provider: a known one, and for
// Cloudflare a valid, unique hostname and no Tailscale settings.
func providerErrors(service ResolvedService, claimed map[string]string) []ValidationError {
	fail := func(field, message string) []ValidationError {
		return []ValidationError{serviceError(service.Name, field, message)}
	}
	switch service.Provider {
	case ProviderTailscale, "":
		if service.hostname != "" {
			return fail("hostname", "works with provider: cloudflare only; Tailscale serves the service at this machine's name")
		}
		return nil
	case ProviderCloudflare:
	default:
		return fail("provider", "must be tailscale or cloudflare")
	}
	if service.TCP() {
		return fail("provider", "cloudflare serves HTTP services only; Cloudflare TCP needs cloudflared on every client")
	}
	var errs []ValidationError
	// One way in: Cloudflare or Tailscale, never both for the same service.
	for field, set := range map[string]bool{"path": service.pathSet, "public": service.publicSet} {
		if set {
			errs = append(errs, serviceError(service.Name, field, "is a Tailscale setting; a service with provider: cloudflare is always public on its hostname, so remove "+field))
		}
	}
	// Without a domain the name is incomplete; domainErrors says so.
	if service.hostname != "" && strings.Contains(service.Cloudflare, ".") {
		if message := invalidHostnameMessage(service.Cloudflare); message != "" {
			return append(errs, serviceError(service.Name, "hostname", message))
		}
	}
	if owner := claimed[service.Cloudflare]; owner != "" {
		return append(errs, serviceError(service.Name, "hostname", fmt.Sprintf("%s is already the hostname of service %q", service.Cloudflare, owner)))
	}
	claimed[service.Cloudflare] = service.Name
	return errs
}

// invalidHostnameMessage explains why host is not a public DNS name.
func invalidHostnameMessage(host string) string {
	switch {
	case strings.Contains(host, "://") || strings.Contains(host, "/") || net.ParseIP(host) != nil:
		return "must be a name such as example.com, not a URL or address"
	case strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".ts.net"):
		return "must be a domain on your Cloudflare account, such as example.com"
	case !strings.Contains(host, "."):
		return "must be a full domain, such as example.com"
	case len(host) > 253:
		return "must be 253 characters or fewer"
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) > 63 || !domainLabel.MatchString(label) {
			return "must use lowercase letters, digits, and hyphens, such as api-v2"
		}
	}
	return ""
}
