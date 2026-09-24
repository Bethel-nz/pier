package config

import "time"

// Config is the project configuration as represented in pier.yaml.
type Config struct {
	Version int    `yaml:"version"`
	Name    string `yaml:"name"`
	// Domain is a domain on your Cloudflare account, such as example.com.
	// Services with provider: cloudflare are served under it.
	Domain   string             `yaml:"domain"`
	Defaults Defaults           `yaml:"defaults"`
	Local    LocalSettings      `yaml:"local"`
	Services map[string]Service `yaml:"services"`
}

// LocalSettings configure how the project's local: names are served.
type LocalSettings struct {
	// LAN defaults to true. false serves the names to this machine only.
	LAN *bool `yaml:"lan"`
	// Autostart serves the names after login without running pier up.
	Autostart bool `yaml:"autostart"`
	// TLS replaces Pier's certificate with your own.
	TLS *TLSFiles `yaml:"tls"`
}

// TLSFiles are a certificate and key, relative to the project root.
type TLSFiles struct {
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

// Defaults contains values inherited by services that omit them.
type Defaults struct {
	Public   bool   `yaml:"public"`
	Protocol string `yaml:"protocol"`
}

// Service is a service entry before defaults are applied.
type Service struct {
	Target   string  `yaml:"target"`
	Path     string  `yaml:"path"`
	Public   *Public `yaml:"public"`
	Protocol string  `yaml:"protocol"`
	// Domain is the optional `local:` name, such as my-app.local, that pier up
	// serves over HTTPS to every device on the local network.
	Domain string `yaml:"local"`
	// Run is an optional shell command pier up starts and keeps running.
	Run string `yaml:"run"`
	// Dir is where Run starts, relative to the project root.
	Dir string `yaml:"dir"`
	// Env adds variables for Run. PORT defaults to the target's port.
	Env map[string]string `yaml:"env"`
	// Watch restarts Run when a file matching one of these globs changes.
	Watch []string `yaml:"watch"`
	// Throttle slows traffic to the service, such as "3g".
	Throttle *Throttle `yaml:"throttle"`
	// Capture keeps requests to the service for pier replay, such as "24h".
	Capture string `yaml:"capture"`
	// Listen is the port a TCP service is reached on, over Tailscale and on
	// the LAN. It defaults to the target's port.
	Listen uint16 `yaml:"listen"`
	// Provider is who serves the service beyond this machine: tailscale (the
	// default) or cloudflare, which serves it publicly under Domain.
	Provider string `yaml:"provider"`
	// Hostname is the name under Domain for a Cloudflare service, such as
	// api-v2 for api-v2.example.com. It defaults to the service name.
	Hostname string `yaml:"hostname"`
}

// Providers serve a service beyond this machine.
const (
	ProviderTailscale  = "tailscale"
	ProviderCloudflare = "cloudflare"
)

// Protocol is how Pier reaches a service: HTTP or HTTPS proxied by path, or
// raw TCP forwarded by port.
type Protocol string

const (
	ProtocolHTTP  Protocol = "http"
	ProtocolHTTPS Protocol = "https"
	ProtocolTCP   Protocol = "tcp"
)

// Project is a normalized Pier project.
type Project struct {
	Version int
	Name    string
	// Domain is the Cloudflare domain services with provider: cloudflare use.
	Domain   string
	Local    Local
	Services []ResolvedService
}

// Local is the normalized local: block. Cert and Key stay as written;
// the caller resolves them against the project root.
type Local struct {
	LAN       bool
	Autostart bool
	CertFile  string
	KeyFile   string
}

// ResolvedService is a service with inherited values and listener allocation applied.
type ResolvedService struct {
	Name   string
	Target string
	Host   string
	Port   uint16
	// HTTPSPort is the Tailscale listener: 443 or 8443 for HTTP services, the
	// listen port for TCP ones.
	HTTPSPort uint16
	// Path is empty for TCP services, which are reached by port alone.
	Path     string
	Protocol Protocol
	Public   bool
	Domain   string
	Run      Run
	// PublicFor is how long Public lasts after each pier up; 0 is until pier down.
	PublicFor time.Duration
	// Throttle is zero at full speed.
	Throttle Shaping
	// Capture is how long requests are kept for replay; 0 captures nothing.
	Capture time.Duration
	// Provider is tailscale or cloudflare.
	Provider string
	// Cloudflare is the public hostname served through the project's
	// Cloudflare Tunnel, such as web.example.com; empty for Tailscale.
	Cloudflare string

	// As written, for Validate to explain.
	throttle      *Throttle
	capture       string
	publicProblem string
	pathSet       bool
	publicSet     bool
	listenSet     bool
	hostname      string
}

// OnTailscale reports whether Pier serves the service on Tailscale. A
// service with provider: cloudflare is served by Cloudflare instead.
func (s ResolvedService) OnTailscale() bool { return s.Provider != ProviderCloudflare }

// Tapped reports whether Pier must see this service's traffic itself: to
// slow it or to record it.
func (s ResolvedService) Tapped() bool { return !s.Throttle.IsZero() || s.Capture > 0 }

// TCP reports whether the service is raw TCP rather than HTTP.
func (s ResolvedService) TCP() bool { return s.Protocol == ProtocolTCP }

// Run is how Pier starts a service. Command is empty when Pier does not run it.
type Run struct {
	Command string
	Dir     string // relative to the project root
	Env     map[string]string
	Watch   []string
}
