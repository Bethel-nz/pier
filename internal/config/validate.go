package config

import (
	"fmt"
	"net"
	"path"
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
				errors = append(errors, serviceError(service.Name, "domain", message))
			} else if owner, exists := claimedDomains[service.Domain]; exists {
				errors = append(errors, serviceError(service.Name, "domain", fmt.Sprintf("duplicates domain claimed by service %q", owner)))
			} else {
				claimedDomains[service.Domain] = service.Name
			}
		}
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
	for _, label := range labels {
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
