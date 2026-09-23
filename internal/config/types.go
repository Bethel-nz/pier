package config

// Config is the project configuration as represented in pier.yaml.
type Config struct {
	Version  int                `yaml:"version"`
	Name     string             `yaml:"name"`
	Defaults Defaults           `yaml:"defaults"`
	Services map[string]Service `yaml:"services"`
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
	Services []ResolvedService
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
}
