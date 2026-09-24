package cloudflare

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// Route sends one public hostname to a loopback URL.
type Route struct {
	Hostname string
	Target   string
}

type configFile struct {
	Tunnel      string        `yaml:"tunnel"`
	Credentials string        `yaml:"credentials-file"`
	Ingress     []ingressRule `yaml:"ingress"`
}

type ingressRule struct {
	Hostname      string         `yaml:"hostname,omitempty"`
	Service       string         `yaml:"service"`
	OriginRequest *originRequest `yaml:"originRequest,omitempty"`
}

type originRequest struct {
	NoTLSVerify bool `yaml:"noTLSVerify"`
}

// Config is the cloudflared config file for tunnel: one ingress rule per
// route, and a 404 for any other hostname. HTTPS targets are local dev
// servers, so their certificates are not checked.
func Config(tunnel Tunnel, routes []Route) ([]byte, error) {
	file := configFile{Tunnel: tunnel.ID, Credentials: tunnel.Credentials}
	for _, route := range routes {
		rule := ingressRule{Hostname: route.Hostname, Service: route.Target}
		if strings.HasPrefix(route.Target, "https://") {
			rule.OriginRequest = &originRequest{NoTLSVerify: true}
		}
		file.Ingress = append(file.Ingress, rule)
	}
	file.Ingress = append(file.Ingress, ingressRule{Service: "http_status:404"})
	return yaml.Marshal(file)
}

// RunArgs are the arguments that run the tunnel from configPath, with its
// readiness endpoint on metrics and its log in logFile.
func RunArgs(configPath, metrics, logFile string) []string {
	return []string{
		"tunnel", "--no-autoupdate", "--config", configPath,
		"--metrics", metrics, "--logfile", logFile, "run",
	}
}
