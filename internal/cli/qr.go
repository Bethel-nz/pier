package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/localproxy"
	"github.com/Bethel-nz/pier/internal/render"
)

func newQRCommand(rt *runtime) *cobra.Command {
	var ca, tailnet bool
	cmd := &cobra.Command{
		Use:   "qr [service]",
		Short: "Show a service's URL as a QR code to open it on a phone",
		Long: "Show a service's .local URL as a QR code. With one local service, the name is optional.\n" +
			"--ca shows the link that installs Pier's CA on a phone; --tailscale shows the Tailscale URL instead.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := rt.app.Status(cmd.Context(), app.StatusRequest{Start: rt.start()})
			if err != nil {
				return rt.renderer("qr").Error(err)
			}
			url, err := qrTarget(status.Services, args, ca, tailnet)
			if err != nil {
				return rt.renderer("qr").Error(err)
			}
			if rt.json {
				return rt.renderer("qr").URL("qr", status.Project, url, nil)
			}
			if err := render.QR(rt.stdout, url, !rt.noColor); err != nil {
				return rt.renderer("qr").Error(err)
			}
			fmt.Fprintln(rt.stdout, url)
			return nil
		},
	}
	cmd.Flags().BoolVar(&ca, "ca", false, "show the link that installs Pier's CA on a phone")
	cmd.Flags().BoolVar(&tailnet, "tailscale", false, "show the Tailscale URL instead of the .local URL")
	return cmd
}

// qrTarget picks the URL to encode. It never falls back silently: a name that
// is not served yet is an error that says why.
func qrTarget(services []app.ServiceInfo, args []string, ca, tailnet bool) (string, error) {
	var chosen *app.ServiceInfo
	if len(args) == 1 {
		for i := range services {
			if services[i].Name == args[0] {
				chosen = &services[i]
			}
		}
		if chosen == nil {
			return "", &app.ServiceNotFoundError{Name: args[0]}
		}
	} else {
		var local []*app.ServiceInfo
		for i := range services {
			if services[i].Domain != "" && (tailnet || services[i].LocalURL != "" || !ca) {
				local = append(local, &services[i])
			}
		}
		if tailnet && len(services) == 1 {
			chosen = &services[0]
		} else if len(local) == 1 || (ca && len(local) > 0) {
			chosen = local[0]
		} else {
			names := make([]string, 0, len(services))
			for _, service := range services {
				names = append(names, service.Name)
			}
			return "", fmt.Errorf("Pier needs a service name: pier qr <%s>", strings.Join(names, "|"))
		}
	}
	switch {
	case tailnet:
		return chosen.URL, nil
	case chosen.Domain == "":
		return "", fmt.Errorf("Pier has no .local name for %q; add local: to it in pier.yaml, or use --tailscale", chosen.Name)
	case chosen.LocalURL == "":
		state := chosen.LocalState
		if state == "" {
			state = "down"
		}
		return "", fmt.Errorf("Pier is not serving %s right now (%s); run pier up", chosen.Domain, state)
	case ca:
		return "http://" + chosen.Domain + localproxy.InstallPath, nil
	default:
		return chosen.LocalURL, nil
	}
}
