package config

import (
	"fmt"
	"net"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var serviceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ValidServiceName reports whether name matches Pier's service-name rule.
func ValidServiceName(name string) bool {
	return serviceNamePattern.MatchString(name)
}

// ValidationError describes one invalid project or service field.
type ValidationError struct {
	Service string
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	field := e.Field
	if e.Service != "" {
		field = "services." + e.Service + "." + e.Field
	}
	return field + ": " + e.Message
}

// Validate returns every semantic configuration error in stable order.
func Validate(project Project) []ValidationError {
	errors := make([]ValidationError, 0)
	if project.Version != 1 {
		errors = append(errors, ValidationError{Field: "version", Message: "must equal 1"})
	}
	if strings.TrimSpace(project.Name) == "" {
		errors = append(errors, ValidationError{Field: "name", Message: "must not be empty"})
	}
	if len(project.Services) == 0 {
		errors = append(errors, ValidationError{Field: "services", Message: "must contain at least one service"})
	}

	claimedRoutes := make(map[string]string, len(project.Services))
	claimedDomains := make(map[string]string, len(project.Services))
	for _, service := range project.Services {
		if !serviceNamePattern.MatchString(service.Name) {
			errors = append(errors, serviceError(service.Name, "name", "must match ^[a-z][a-z0-9-]*$"))
		}
		if !validTarget(service) {
			errors = append(errors, serviceError(service.Name, "target", "must be a loopback host with a valid port"))
		}
		if message := invalidPathMessage(service.Path); message != "" {
			errors = append(errors, serviceError(service.Name, "path", message))
		} else if route := fmt.Sprintf("https:%d:%s", service.HTTPSPort, service.Path); claimedRoutes[route] != "" {
			errors = append(errors, serviceError(service.Name, "path", fmt.Sprintf("duplicates listener and path claimed by service %q", claimedRoutes[route])))
		} else {
			claimedRoutes[route] = service.Name
		}
		if service.Protocol != ProtocolHTTP && service.Protocol != ProtocolHTTPS {
			errors = append(errors, serviceError(service.Name, "protocol", "must be http or https"))
		}
		if service.Domain != "" {
			if message := invalidDomainMessage(service.Domain); message != "" {
				errors = append(errors, serviceError(service.Name, "local", message))
			} else if owner, exists := claimedDomains[service.Domain]; exists {
				errors = append(errors, serviceError(service.Name, "local", fmt.Sprintf("duplicates the local name of service %q", owner)))
			} else {
				claimedDomains[service.Domain] = service.Name
			}
		}
		errors = append(errors, runErrors(service)...)
		if _, err := resolveThrottle(service.throttle); err != nil {
			errors = append(errors, serviceError(service.Name, "throttle", err.Error()))
		}
		if service.publicProblem != "" {
			errors = append(errors, serviceError(service.Name, "public", service.publicProblem))
		}
		if _, err := resolveCapture(service.capture); err != nil {
			errors = append(errors, serviceError(service.Name, "capture", err.Error()))
		}
	}
	if (project.Local.CertFile == "") != (project.Local.KeyFile == "") {
		errors = append(errors, ValidationError{Field: "local.tls", Message: "needs both cert and key"})
	}

	sort.SliceStable(errors, func(i, j int) bool {
		if errors[i].Service != errors[j].Service {
			return errors[i].Service < errors[j].Service
		}
		if errors[i].Field != errors[j].Field {
			return errors[i].Field < errors[j].Field
		}
		return errors[i].Message < errors[j].Message
	})
	return errors
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func runErrors(service ResolvedService) []ValidationError {
	var errors []ValidationError
	run := service.Run
	if run.Command == "" {
		for field, set := range map[string]bool{"dir": run.Dir != "", "env": len(run.Env) > 0, "watch": len(run.Watch) > 0} {
			if set {
				errors = append(errors, serviceError(service.Name, field, "needs run"))
			}
		}
		return errors
	}
	if filepath.IsAbs(run.Dir) || strings.HasPrefix(filepath.ToSlash(filepath.Clean(run.Dir)), "../") {
		errors = append(errors, serviceError(service.Name, "dir", "must be a folder inside the project"))
	}
	for name := range run.Env {
		if !envName.MatchString(name) {
			errors = append(errors, serviceError(service.Name, "env", fmt.Sprintf("%q is not a valid variable name", name)))
		}
	}
	for _, pattern := range run.Watch {
		if pattern == "" || path.IsAbs(pattern) || strings.HasPrefix(pattern, "../") {
			errors = append(errors, serviceError(service.Name, "watch", fmt.Sprintf("%q must be a glob inside dir", pattern)))
		} else if _, err := path.Match(strings.ReplaceAll(pattern, "**", "*"), ""); err != nil {
			errors = append(errors, serviceError(service.Name, "watch", fmt.Sprintf("%q is not a valid glob", pattern)))
		}
	}
	return errors
}

func serviceError(service, field, message string) ValidationError {
	return ValidationError{Service: service, Field: field, Message: message}
}

func validTarget(service ResolvedService) bool {
	if service.Port == 0 || service.Host == "" {
		return false
	}
	if strings.EqualFold(service.Host, "localhost") {
		return true
	}
	ip := net.ParseIP(service.Host)
	return ip != nil && ip.IsLoopback()
}

func invalidDomainMessage(domain string) string {
	if strings.Contains(domain, "://") || net.ParseIP(domain) != nil {
		return "must be a hostname ending in .local, not an address"
	}
	if !strings.HasSuffix(domain, ".local") {
		return "must end in .local"
	}
	labels := strings.Split(strings.TrimSuffix(domain, ".local"), ".")
	if len(labels) == 0 || labels[0] == "" {
		return "must have a name before .local"
	}
	if len(domain) > 253 {
		return "must be 253 characters or fewer"
	}
	for _, label := range labels {
		if len(label) > 63 {
			return "each label must be 63 characters or fewer"
		}
		if !domainLabel.MatchString(label) {
			return "must use lowercase letters, digits, and hyphens"
		}
	}
	return ""
}

var domainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

func invalidPathMessage(servicePath string) string {
	if !strings.HasPrefix(servicePath, "/") {
		return "must start with /"
	}
	if strings.ContainsAny(servicePath, "?#") {
		return "must not contain a query or fragment"
	}
	if path.Clean(servicePath) != servicePath {
		return "must be path-clean"
	}
	return ""
}
