package render

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/localname"
	"github.com/Bethel-nz/pier/internal/localproxy"
)

// JSONLocal is .local serving state for up, status, and doctor.
type JSONLocal struct {
	Running       bool     `json:"running"`
	HTTPSPort     int      `json:"httpsPort,omitempty"`
	CAPath        string   `json:"caPath,omitempty"`
	CAFingerprint string   `json:"caFingerprint,omitempty"`
	CATrusted     bool     `json:"caTrusted"`
	CertDir       string   `json:"certDir,omitempty"`
	APIURL        string   `json:"apiUrl,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

func jsonLocal(services []app.ServiceInfo, report localname.Report) *JSONLocal {
	if !anyDomain(services) {
		return nil
	}
	return &JSONLocal{
		Running:       report.Running,
		HTTPSPort:     report.HTTPSPort,
		CAPath:        report.CAPath,
		CAFingerprint: report.CAFingerprint,
		CATrusted:     report.CATrusted,
		CertDir:       report.CertDir,
		APIURL:        report.APIURL,
		Warnings:      localWarnings(report),
	}
}

func anyDomain(services []app.ServiceInfo) bool {
	for _, service := range services {
		if service.Domain != "" {
			return true
		}
	}
	return false
}

// writeLocalNames prints one line per domain: its URL when live, otherwise
// why it is not served.
func writeLocalNames(w io.Writer, services []app.ServiceInfo) {
	for _, service := range services {
		if service.Domain == "" {
			continue
		}
		if service.LocalURL != "" {
			fmt.Fprintf(w, "local  %s  %s\n", service.Name, service.LocalURL)
			continue
		}
		state := service.LocalState
		if state == "" {
			state = "down"
		}
		fmt.Fprintf(w, "local  %s  %s  (%s)\n", service.Name, service.Domain, state)
	}
}

// writeLocalSetup prints certificate, trust, and warning lines after pier up.
func writeLocalSetup(w io.Writer, services []app.ServiceInfo, report localname.Report) {
	if !anyDomain(services) {
		return
	}
	for _, service := range services {
		if service.LocalState == localname.StateConflict || service.LocalState == localname.StateNoCertificate {
			if status, ok := report.Names[service.Domain]; ok && status.Detail != "" {
				fmt.Fprintf(w, "  %s: %s\n", service.Domain, status.Detail)
			}
		}
	}
	if report.CertIssued {
		fmt.Fprintf(w, "certificate  %s\n", filepath.Join(report.CertDir, "cert.pem"))
	}
	switch {
	case report.CAPath == "":
	case report.CATrusted:
		if report.TrustedNow {
			fmt.Fprintln(w, "trusted      Pier Local CA added to this machine's trust store")
		}
	case report.TrustError != "":
		fmt.Fprintf(w, "trust        Pier could not trust its CA (%s). Run pier trust to retry\n", strings.TrimSuffix(report.TrustError, "."))
	default:
		fmt.Fprintln(w, "trust        Pier Local CA is not trusted yet, so browsers will warn. Run pier trust once")
	}
	if report.CACreated || report.TrustedNow {
		if phone := firstLive(services); phone != "" {
			fmt.Fprintf(w, "phones       open http://%s%s once on each other device to trust Pier\n", phone, localproxy.InstallPath)
		}
	}
	for _, warning := range localWarnings(report) {
		fmt.Fprintf(w, "warning      %s\n", warning)
	}
}

func firstLive(services []app.ServiceInfo) string {
	for _, service := range services {
		if service.LocalURL != "" {
			return service.Domain
		}
	}
	return ""
}

func localWarnings(report localname.Report) []string {
	return append([]string(nil), report.Warnings...)
}

// writeLocalDoctor prints the Local names section of pier doctor.
func writeLocalDoctor(w io.Writer, domains []string, report localname.Report) {
	fmt.Fprintln(w, "Local names")
	writeDoctorFlag(w, "daemon", report.Running)
	writeDoctorFlag(w, "caTrusted", report.CATrusted)
	writeDoctorFlag(w, "autostart", report.Autostart)
	if report.CAPath != "" {
		fmt.Fprintf(w, "  ca: %s\n", report.CAPath)
		fmt.Fprintf(w, "  caFingerprint: %s\n", report.CAFingerprint)
	}
	if report.Running && report.HTTPSPort != 0 {
		fmt.Fprintf(w, "  httpsPort: %d\n", report.HTTPSPort)
	}
	if report.APIURL != "" {
		fmt.Fprintf(w, "  api: %s\n", report.APIURL)
	}
	for _, domain := range domains {
		status, ok := report.Names[domain]
		state := report.State(domain)
		line := fmt.Sprintf("  %s: %s", domain, state)
		if ok && status.Detail != "" {
			line += " (" + status.Detail + ")"
		}
		fmt.Fprintln(w, line)
	}
	for _, warning := range localWarnings(report) {
		fmt.Fprintf(w, "  warning: %s\n", warning)
	}
	if !report.Running {
		fmt.Fprintln(w, "  run pier up to serve .local names")
	}
}
