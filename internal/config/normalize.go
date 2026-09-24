package config

import (
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Normalize applies inherited values and produces a deterministic service list.
func Normalize(cfg Config) (Project, error) {
	project := Project{
		Version: cfg.Version,
		Name:    cfg.Name,
		Local:   Local{LAN: true, Autostart: cfg.Local.Autostart},
	}
	if cfg.Local.LAN != nil {
		project.Local.LAN = *cfg.Local.LAN
	}
	if cfg.Local.TLS != nil {
		project.Local.CertFile = strings.TrimSpace(cfg.Local.TLS.Cert)
		project.Local.KeyFile = strings.TrimSpace(cfg.Local.TLS.Key)
	}

	names := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		service := cfg.Services[name]
		protocol := service.Protocol
		if protocol == "" {
			protocol = cfg.Defaults.Protocol
		}
		if protocol == "" {
			protocol = string(ProtocolHTTP)
		}

		public := cfg.Defaults.Public
		var publicFor time.Duration
		if service.Public != nil {
			public = service.Public.On
			publicFor = service.Public.For
		}

		servicePath := service.Path
		if servicePath == "" {
			servicePath = "/"
		} else if servicePath != "/" && strings.HasSuffix(servicePath, "/") && !strings.HasSuffix(servicePath, "//") {
			servicePath = strings.TrimSuffix(servicePath, "/")
		}

		target, host, port := resolveTarget(service.Target, Protocol(protocol))
		shaping, _ := resolveThrottle(service.Throttle) // Validate reports a bad one
		keep, _ := resolveCapture(strings.TrimSpace(service.Capture))
		httpsPort := uint16(8443)
		if public {
			httpsPort = 443
		}
		if Protocol(protocol) == ProtocolTCP {
			// Reached by port alone, on the target's port unless listen says otherwise.
			httpsPort, servicePath = port, ""
			if service.Listen != 0 {
				httpsPort = service.Listen
			}
		}

		project.Services = append(project.Services, ResolvedService{
			Name:       name,
			Target:     target,
			Host:       host,
			Port:       port,
			HTTPSPort:  httpsPort,
			Path:       servicePath,
			Protocol:   Protocol(protocol),
			Public:     public,
			Domain:     normalizeDomain(service.Domain),
			Cloudflare: normalizeDomain(service.Cloudflare),
			Run: Run{
				Command: strings.TrimSpace(service.Run),
				Dir:     strings.TrimSpace(service.Dir),
				Env:     service.Env,
				Watch:   service.Watch,
			},
			PublicFor:     publicFor,
			Throttle:      shaping,
			Capture:       keep,
			throttle:      service.Throttle,
			capture:       strings.TrimSpace(service.Capture),
			publicProblem: service.Public.problem(),
			pathSet:       Protocol(protocol) == ProtocolTCP && strings.TrimSpace(service.Path) != "" && strings.TrimSpace(service.Path) != "/",
			listenSet:     service.Listen != 0,
		})
	}

	return project, nil
}

func resolveTarget(target string, protocol Protocol) (string, string, uint16) {
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return target, "", 0
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return target, host, 0
	}
	if strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}

	return string(protocol) + "://" + net.JoinHostPort(host, portText), host, uint16(port)
}

func normalizeDomain(domain string) string {
	domain = strings.TrimSpace(strings.ToLower(domain))
	return strings.TrimSuffix(domain, ".")
}
