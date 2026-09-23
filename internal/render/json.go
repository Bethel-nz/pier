package render

import (
	"encoding/json"
	"io"
	"strconv"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/config"
	"github.com/Bethel-nz/pier/internal/health"
	"github.com/Bethel-nz/pier/internal/project"
	"github.com/Bethel-nz/pier/internal/reconcile"
)

const schemaVersion = 1

// Envelope is the stable machine-readable output wrapper.
type Envelope struct {
	Version  int          `json:"version"`
	Command  string       `json:"command"`
	Project  *JSONProject `json:"project"`
	Data     any          `json:"data"`
	Warnings []string     `json:"warnings"`
	Errors   []JSONError  `json:"errors"`
}

// JSONProject is the project identity included in every envelope.
type JSONProject struct {
	ID   string `json:"id,omitempty"`
	Path string `json:"path,omitempty"`
}

// JSONError is one explicit error object.
type JSONError struct {
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// JSONValidate is the validate command payload.
type JSONValidate struct {
	Valid  bool             `json:"valid"`
	Errors []JSONFieldError `json:"errors"`
}

// JSONFieldError is one configuration validation finding.
type JSONFieldError struct {
	Service string `json:"service,omitempty"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// JSONPlan is the plan command payload.
type JSONPlan struct {
	Operations []JSONOperation `json:"operations"`
	Conflicts  []JSONConflict  `json:"conflicts"`
}

// JSONOperation is one planned route change.
type JSONOperation struct {
	Kind   string    `json:"kind"`
	Before JSONRoute `json:"before"`
	After  JSONRoute `json:"after"`
}

// JSONRoute is a stable route snapshot.
type JSONRoute struct {
	Service   string `json:"service,omitempty"`
	HTTPSPort uint16 `json:"httpsPort,omitempty"`
	Path      string `json:"path,omitempty"`
	Target    string `json:"target,omitempty"`
	Public    bool   `json:"public"`
}

// JSONConflict is an unmanaged route disagreement.
type JSONConflict struct {
	Desired  JSONRoute `json:"desired"`
	Actual   JSONRoute `json:"actual"`
	Takeover bool      `json:"takeover"`
}

// JSONStatus is the status command payload.
type JSONStatus struct {
	DNSName  string        `json:"dnsName"`
	Services []JSONService `json:"services"`
}

// JSONService is one service row.
type JSONService struct {
	Name      string `json:"name"`
	Target    string `json:"target"`
	Path      string `json:"path"`
	Public    bool   `json:"public"`
	Paused    bool   `json:"paused"`
	Health    string `json:"health"`
	URL       string `json:"url"`
	LocalURL  string `json:"localUrl,omitempty"`
	HTTPSPort uint16 `json:"httpsPort"`
}

// JSONDoctor is the doctor command payload.
type JSONDoctor struct {
	Installed     bool     `json:"installed"`
	DaemonRunning bool     `json:"daemonRunning"`
	Authenticated bool     `json:"authenticated"`
	MagicDNS      bool     `json:"magicDNS"`
	HTTPS         bool     `json:"https"`
	Funnel        bool     `json:"funnel"`
	Tailscale     string   `json:"tailscale,omitempty"`
	Health        []string `json:"health,omitempty"`
	Validation    []string `json:"validation,omitempty"`
}

func writeJSON(w io.Writer, command string, proj project.Context, data any, warnings []string, errs []JSONError) error {
	if warnings == nil {
		warnings = []string{}
	}
	if errs == nil {
		errs = []JSONError{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(Envelope{
		Version:  schemaVersion,
		Command:  command,
		Project:  jsonProject(proj),
		Data:     data,
		Warnings: warnings,
		Errors:   errs,
	})
}

func jsonProject(proj project.Context) *JSONProject {
	if proj.ID == "" && proj.Root == "" {
		return nil
	}
	return &JSONProject{ID: proj.ID, Path: proj.Root}
}

func jsonFieldErrors(errs []config.ValidationError) []JSONFieldError {
	out := make([]JSONFieldError, 0, len(errs))
	for _, err := range errs {
		out = append(out, JSONFieldError{Service: err.Service, Field: err.Field, Message: err.Message})
	}
	return out
}

func jsonPlan(plan reconcile.Plan) JSONPlan {
	ops := make([]JSONOperation, 0, len(plan.Operations))
	for _, op := range plan.Operations {
		ops = append(ops, JSONOperation{Kind: string(op.Kind), Before: jsonRoute(op.Before), After: jsonRoute(op.After)})
	}
	conflicts := make([]JSONConflict, 0, len(plan.Conflicts))
	for _, conflict := range plan.Conflicts {
		conflicts = append(conflicts, JSONConflict{Desired: jsonRoute(conflict.Desired), Actual: jsonRoute(conflict.Actual), Takeover: conflict.Takeover})
	}
	return JSONPlan{Operations: ops, Conflicts: conflicts}
}

func jsonRoute(route reconcile.Route) JSONRoute {
	return JSONRoute{
		Service:   route.Service,
		HTTPSPort: route.HTTPSPort,
		Path:      route.Path,
		Target:    route.Target,
		Public:    route.Public,
	}
}

func jsonServices(services []app.ServiceInfo) []JSONService {
	out := make([]JSONService, 0, len(services))
	for _, service := range services {
		out = append(out, JSONService{
			Name:      service.Name,
			Target:    displayTarget(service),
			Path:      service.Path,
			Public:    service.Public,
			Paused:    service.Paused,
			Health:    string(service.Health.Status),
			URL:       service.URL,
			LocalURL:  service.LocalURL,
			HTTPSPort: service.HTTPSPort,
		})
	}
	return out
}

func jsonDoctor(result app.DoctorResult) JSONDoctor {
	payload := JSONDoctor{
		Installed:     result.Capabilities.Installed,
		DaemonRunning: result.Capabilities.DaemonRunning,
		Authenticated: result.Capabilities.Authenticated,
		MagicDNS:      result.Capabilities.MagicDNS,
		HTTPS:         result.Capabilities.HTTPS,
		Funnel:        result.Capabilities.Funnel,
	}
	if result.TailscaleErr != nil {
		payload.Tailscale = result.TailscaleErr.Error()
	}
	for _, item := range result.Health {
		if item.Status != health.StatusHealthy {
			payload.Health = append(payload.Health, item.Service+": "+string(item.Status))
		}
	}
	for _, item := range result.Validation {
		payload.Validation = append(payload.Validation, item.Error())
	}
	return payload
}

func jsonErrs(err error) []JSONError {
	if err == nil {
		return []JSONError{}
	}
	return []JSONError{{Message: err.Error(), Details: verboseDetails(err)}}
}

func displayTarget(service app.ServiceInfo) string {
	if service.Host != "" && service.Port != 0 {
		return service.Host + ":" + strconv.FormatUint(uint64(service.Port), 10)
	}
	return service.Target
}
