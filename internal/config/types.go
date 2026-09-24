package config

// Config is the project configuration as represented in pier.yaml.
type Config struct {
	Version  int                `yaml:"version"`
	Name     string             `yaml:"name"`
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
	Target   string `yaml:"target"`
	Path     string `yaml:"path"`
	Public   *bool  `yaml:"public"`
	Protocol string `yaml:"protocol"`
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
}

// Protocol is the supported local proxy protocol.
type Protocol string

const (
	ProtocolHTTP  Protocol = "http"
	ProtocolHTTPS Protocol = "https"
)

// Project is a normalized Pier project.
type Project struct {
	Version  int
	Name     string
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
	Name      string
	Target    string
	Host      string
	Port      uint16
	HTTPSPort uint16
	Path      string
	Protocol  Protocol
	Public    bool
	Domain    string
	Run       Run
}

// Run is how Pier starts a service. Command is empty when Pier does not run it.
type Run struct {
	Command string
	Dir     string // relative to the project root
	Env     map[string]string
	Watch   []string
}
