package render

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/health"
	"github.com/Bethel-nz/pier/internal/project"
	"github.com/Bethel-nz/pier/internal/reconcile"
)

// Options control how command results are printed.
type Options struct {
	JSON    bool
	Verbose bool
	Command string
	Out     io.Writer
	Err     io.Writer
}

type verboseDetailer interface {
	VerboseDetails() string
}

func verboseDetails(err error) string {
	var details verboseDetailer
	if errors.As(err, &details) {
		return details.VerboseDetails()
	}
	return ""
}

// Error prints a Pier explanation first and raw Tailscale stderr only when verbose.
func (o Options) Error(err error) error {
	if err == nil {
		return nil
	}
	if o.JSON {
		return writeJSON(o.Out, o.Command, project.Context{}, map[string]any{}, nil, jsonErrs(err))
	}
	fmt.Fprintln(o.Err, err.Error())
	if o.Verbose {
		if details := verboseDetails(err); details != "" {
			fmt.Fprintln(o.Err, details)
		}
	}
	var conflicts *app.ConflictError
	if errors.As(err, &conflicts) {
		writeConflicts(o.Err, conflicts.Conflicts)
	}
	var invalid *app.InvalidConfigError
	if errors.As(err, &invalid) {
		for _, item := range invalid.Errors {
			fmt.Fprintln(o.Err, item.Error())
		}
	}
	return err
}

// Validate renders configuration validation.
func (o Options) Validate(result app.ValidateResult, err error) error {
	if o.JSON {
		payload := JSONValidate{Valid: err == nil && len(result.Errors) == 0, Errors: jsonFieldErrors(result.Errors)}
		if writeErr := writeJSON(o.Out, "validate", result.Project, payload, nil, jsonErrs(err)); writeErr != nil {
			return writeErr
		}
		return err
	}
	if err != nil {
		return o.Error(err)
	}
	fmt.Fprintln(o.Out, "configuration is valid")
	return nil
}

// Plan renders a non-mutating reconciliation plan.
func (o Options) Plan(result app.PlanResult, err error) error {
	if o.JSON {
		if writeErr := writeJSON(o.Out, "plan", result.Project, jsonPlan(result.Plan), nil, jsonErrs(err)); writeErr != nil {
			return writeErr
		}
		return err
	}
	if err != nil {
		return o.Error(err)
	}
	if result.TailscaleSkipped != "" {
		writeTailscaleSkipped(o.Out, result.TailscaleSkipped)
		return nil
	}
	if len(result.Plan.Operations) == 0 && len(result.Plan.Conflicts) == 0 {
		fmt.Fprintln(o.Out, "no changes")
		return nil
	}
	writeOperations(o.Out, result.Plan.Operations)
	writeConflicts(o.Out, result.Plan.Conflicts)
	return nil
}

// Up renders apply results.
func (o Options) Up(result app.UpResult, err error) error {
	if o.JSON {
		payload := JSONStatus{Services: jsonServices(result.Services), Local: jsonLocal(result.Services, result.Local)}
		warnings := append(warningsFromPlan(result.Plan), result.Warnings...)
		if result.TailscaleSkipped != "" {
			warnings = append(warnings, "tailscale skipped: "+result.TailscaleSkipped)
		}
		if writeErr := writeJSON(o.Out, "up", result.Project, payload, warnings, jsonErrs(err)); writeErr != nil {
			return writeErr
		}
		return err
	}
	if err != nil {
		if anyDomain(result.Services) && len(result.Services) > 0 {
			writeLocalSetup(o.Err, result.Services, result.Local)
		}
		return o.Error(err)
	}
	switch {
	case result.TailscaleSkipped != "":
	case !hasMutations(result.Plan):
		fmt.Fprintln(o.Out, "already up")
	default:
		writeOperations(o.Out, result.Plan.Operations)
	}
	writeServiceTable(o.Out, result.Services)
	writeLocalSetup(o.Out, result.Services, result.Local)
	writeTailscaleSkipped(o.Out, result.TailscaleSkipped)
	writeWarnings(o.Out, result.Warnings)
	return nil
}

// Down renders owned-route deletion.
func (o Options) Down(result app.DownResult, err error) error {
	if o.JSON {
		if writeErr := writeJSON(o.Out, "down", result.Project, jsonPlan(result.Plan), nil, jsonErrs(err)); writeErr != nil {
			return writeErr
		}
		return err
	}
	if err != nil {
		return o.Error(err)
	}
	if result.TailscaleSkipped != "" {
		fmt.Fprintln(o.Out, "local names withdrawn")
		if result.KeptRoutes > 0 {
			writeTailscaleSkipped(o.Out, fmt.Sprintf("%s; %d Tailscale route(s) stay until pier down runs with Tailscale up", result.TailscaleSkipped, result.KeptRoutes))
		}
		return nil
	}
	if !hasMutations(result.Plan) {
		fmt.Fprintln(o.Out, "no owned routes")
		return nil
	}
	writeOperations(o.Out, result.Plan.Operations)
	return nil
}

// Status renders configured services.
func (o Options) Status(result app.StatusResult, err error) error {
	if o.JSON {
		payload := JSONStatus{DNSName: result.DNSName, Services: jsonServices(result.Services), Local: jsonLocal(result.Services, result.Local)}
		warnings := append([]string(nil), result.Warnings...)
		if result.TailscaleSkipped != "" {
			warnings = append(warnings, "tailscale skipped: "+result.TailscaleSkipped)
		}
		if writeErr := writeJSON(o.Out, "status", result.Project, payload, warnings, jsonErrs(err)); writeErr != nil {
			return writeErr
		}
		return err
	}
	if err != nil {
		return o.Error(err)
	}
	writeServiceTable(o.Out, result.Services)
	writeDrift(o.Out, result.Services)
	writeTailscaleSkipped(o.Out, result.TailscaleSkipped)
	writeWarnings(o.Out, result.Warnings)
	return nil
}

// Machine renders every Tailscale route on this machine, public ones first.
func (o Options) Machine(result app.MachineResult, err error) error {
	if o.JSON {
		items := make([]JSONMachineRoute, 0, len(result.Routes))
		for _, route := range result.Routes {
			items = append(items, jsonMachineRoute(route))
		}
		if writeErr := writeJSON(o.Out, "status", project.Context{}, map[string]any{"dnsName": result.DNSName, "routes": items}, nil, jsonErrs(err)); writeErr != nil {
			return writeErr
		}
		return err
	}
	if err != nil {
		return o.Error(err)
	}
	if len(result.Routes) == 0 {
		fmt.Fprintln(o.Out, "Tailscale serves nothing on this machine")
		return nil
	}
	tab := tabwriter.NewWriter(o.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tab, "ACCESS\tURL\tTARGET\tOWNER")
	public := 0
	for _, route := range result.Routes {
		access := "tailnet"
		if route.Route.Public {
			access = publicLabel(route.Since)
			public++
		}
		owner := "-"
		if route.Project != "" {
			owner = route.Service + " (" + route.Project + ")"
		}
		fmt.Fprintf(tab, "%s\t%s\t%s\t%s\n", access, orDash(route.URL), route.Route.Target, owner)
	}
	_ = tab.Flush()
	if public > 0 {
		fmt.Fprintf(o.Out, "%d route(s) are PUBLIC on the internet\n", public)
	}
	return nil
}

// publicColumn is loud for a service anyone on the internet can reach.
func publicColumn(service app.ServiceInfo) string {
	if !service.Public {
		return "no"
	}
	return publicLabel(service.PublicSince)
}

// publicLabel is PUBLIC, with how long when Pier knows it.
func publicLabel(since time.Time) string {
	if since.IsZero() {
		return "PUBLIC"
	}
	return "PUBLIC " + app.Age(time.Since(since))
}

// writeDrift prints where Tailscale differs from pier.yaml, with the fix.
func writeDrift(w io.Writer, services []app.ServiceInfo) {
	for _, service := range services {
		for _, drift := range service.Drift {
			fmt.Fprintf(w, "drift        %s: %s\n", service.Name, drift)
		}
	}
}

func writeWarnings(w io.Writer, warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintf(w, "warning      %s\n", warning)
	}
}

// Doctor renders diagnostics.
func (o Options) Doctor(result app.DoctorResult, err error) error {
	if o.JSON {
		if writeErr := writeJSON(o.Out, "doctor", result.Project, jsonDoctor(result), result.Warnings, jsonErrs(err)); writeErr != nil {
			return writeErr
		}
		return err
	}
	if err != nil && result.Project.ID == "" {
		return o.Error(err)
	}
	fmt.Fprintln(o.Out, "Tailscale")
	writeDoctorFlag(o.Out, "installed", result.Capabilities.Installed)
	writeDoctorFlag(o.Out, "daemon", result.Capabilities.DaemonRunning)
	writeDoctorFlag(o.Out, "authenticated", result.Capabilities.Authenticated)
	writeDoctorFlag(o.Out, "magicDNS", result.Capabilities.MagicDNS)
	writeDoctorFlag(o.Out, "https", result.Capabilities.HTTPS)
	writeDoctorFlag(o.Out, "funnel", result.Capabilities.Funnel)
	if result.TailscaleErr != nil {
		fmt.Fprintf(o.Out, "  error: %s\n", result.TailscaleErr.Error())
		if o.Verbose {
			if details := verboseDetails(result.TailscaleErr); details != "" {
				fmt.Fprintf(o.Out, "  details: %s\n", details)
			}
		}
	}
	if len(result.Validation) > 0 {
		fmt.Fprintln(o.Out, "Configuration")
		for _, item := range result.Validation {
			fmt.Fprintf(o.Out, "  %s\n", item.Error())
		}
	}
	if len(result.Health) > 0 {
		fmt.Fprintln(o.Out, "Targets")
		for _, item := range result.Health {
			fmt.Fprintf(o.Out, "  %s  %s\n", item.Service, item.Status)
		}
	}
	if result.Local != nil {
		writeLocalDoctor(o.Out, doctorDomains(result), *result.Local)
	}
	if len(result.Warnings) > 0 {
		fmt.Fprintln(o.Out, "Warnings")
		for _, warning := range result.Warnings {
			fmt.Fprintf(o.Out, "  %s\n", warning)
		}
	}
	return err
}

func doctorDomains(result app.DoctorResult) []string {
	var domains []string
	for _, service := range result.Config.Services {
		if service.Domain != "" {
			domains = append(domains, service.Domain)
		}
	}
	return domains
}

// Share renders one shared service.
func (o Options) Share(result app.ShareResult, err error) error {
	return o.serviceAction("share", result.Project, result.Service, result.Plan, err)
}

// Unshare renders one unshared service.
func (o Options) Unshare(result app.UnshareResult, err error) error {
	return o.serviceAction("unshare", result.Project, result.Service, result.Plan, err)
}

// Pause renders one paused service.
func (o Options) Pause(result app.PauseResult, err error) error {
	return o.serviceAction("pause", result.Project, result.Service, result.Plan, err)
}

// Resume renders one resumed service.
func (o Options) Resume(result app.ResumeResult, err error) error {
	return o.serviceAction("resume", result.Project, result.Service, result.Plan, err)
}

// Add renders a newly added service.
func (o Options) Add(result app.AddServiceResult, err error) error {
	return o.serviceAction("service add", result.Project, result.Service, reconcile.Plan{}, err)
}

// JSONData writes data in the standard --json envelope and returns err.
func (o Options) JSONData(command string, proj project.Context, data any, err error) error {
	if writeErr := writeJSON(o.Out, command, proj, data, nil, jsonErrs(err)); writeErr != nil {
		return writeErr
	}
	return err
}

// URL renders a resolved service URL.
func (o Options) URL(command string, proj project.Context, url string, err error) error {
	if o.JSON {
		if writeErr := writeJSON(o.Out, command, proj, map[string]string{"url": url}, nil, jsonErrs(err)); writeErr != nil {
			return writeErr
		}
		return err
	}
	if err != nil {
		return o.Error(err)
	}
	fmt.Fprintln(o.Out, url)
	return nil
}

func (o Options) serviceAction(command string, proj project.Context, service app.ServiceInfo, plan reconcile.Plan, err error) error {
	if o.JSON {
		payload := JSONStatus{Services: jsonServices([]app.ServiceInfo{service})}
		if writeErr := writeJSON(o.Out, command, proj, payload, warningsFromPlan(plan), jsonErrs(err)); writeErr != nil {
			return writeErr
		}
		return err
	}
	if err != nil {
		return o.Error(err)
	}
	writeServiceTable(o.Out, []app.ServiceInfo{service})
	return nil
}

func writeServiceTable(w io.Writer, services []app.ServiceInfo) {
	tab := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tab, "SERVICE\tTARGET\tPATH\tPUBLIC\tPAUSED\tHEALTH\tURL")
	for _, service := range services {
		healthStatus := string(service.Health.Status)
		if healthStatus == "" {
			healthStatus = string(health.StatusUnavailable)
		}
		fmt.Fprintf(tab, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			service.Name,
			displayTarget(service),
			service.Path,
			publicColumn(service),
			strconv.FormatBool(service.Paused),
			healthStatus,
			orDash(service.URL),
		)
	}
	_ = tab.Flush()
	writeLocalNames(w, services)
}

// writeTailscaleSkipped explains that Tailscale was left alone and why.
func writeTailscaleSkipped(w io.Writer, reason string) {
	if reason == "" {
		return
	}
	fmt.Fprintf(w, "tailscale    skipped: %s\n", strings.TrimPrefix(reason, "Pier cannot use Tailscale: "))
}

func writeOperations(w io.Writer, ops []reconcile.Operation) {
	for _, op := range ops {
		route := op.After
		if op.Kind == reconcile.KindDelete {
			route = op.Before
		}
		visibility := "tailnet"
		if route.Public {
			visibility = "public"
		}
		fmt.Fprintf(w, "%s  %s  %s  %s\n", op.Kind, route.Key(), route.Target, visibility)
	}
}

func writeConflicts(w io.Writer, conflicts []reconcile.Conflict) {
	for _, conflict := range conflicts {
		fmt.Fprintf(w, "conflict  desired %s %s  actual %s %s\n",
			conflict.Desired.Key(), conflict.Desired.Target,
			conflict.Actual.Key(), conflict.Actual.Target,
		)
	}
	if len(conflicts) > 0 {
		fmt.Fprintln(w, "use pier up --force to take over unmanaged routes")
	}
}

func writeDoctorFlag(w io.Writer, name string, ok bool) {
	fmt.Fprintf(w, "  %s: %s\n", name, strconv.FormatBool(ok))
}

func hasMutations(plan reconcile.Plan) bool {
	for _, op := range plan.Operations {
		if op.Kind != reconcile.KindKeep {
			return true
		}
	}
	return false
}

func warningsFromPlan(plan reconcile.Plan) []string {
	if len(plan.Conflicts) == 0 {
		return []string{}
	}
	return []string{"unmanaged Tailscale routes were taken over"}
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
