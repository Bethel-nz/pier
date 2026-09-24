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
	var ca, tailnet, lan bool
	cmd := &cobra.Command{
		Use:   "qr [service]",
		Short: "Show a service's URL as a QR code to open it on a phone",
		Long: "Show a service's .local URL as a QR code. With one local service, the name is optional.\n" +
			"--lan shows the plain-HTTP address on this machine's IP, which works on any network with nothing to install;\n" +
			"--ca shows the link that installs Pier's CA on a phone; --tailscale shows the Tailscale URL instead.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := rt.app.Status(cmd.Context(), app.StatusRequest{Start: rt.start()})
			if err != nil {
				return rt.renderer("qr").Error(err)
			}
			url, err := qrTarget(status.Services, args, qrChoice{ca: ca, tailnet: tailnet, lan: lan})
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
	cmd.Flags().BoolVar(&lan, "lan", false, "show the plain-HTTP LAN address, which needs no .local lookup or certificate")
	cmd.Flags().BoolVar(&ca, "ca", false, "show the link that installs Pier's CA on a phone")
	cmd.Flags().BoolVar(&tailnet, "tailscale", false, "show the Tailscale URL instead of the .local URL")
	return cmd
}

// qrChoice is which of a service's addresses to encode.
type qrChoice struct {
	ca, tailnet, lan bool
}

// qrTarget picks the URL to encode. It never falls back silently: a name that
// is not served yet is an error that says why.
func qrTarget(services []app.ServiceInfo, args []string, choice qrChoice) (string, error) {
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
			if services[i].Domain != "" && (choice.tailnet || services[i].LocalURL != "" || !choice.ca) {
				local = append(local, &services[i])
			}
		}
		if choice.tailnet && len(services) == 1 {
			chosen = &services[0]
		} else if len(local) == 1 || (choice.ca && len(local) > 0) {
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
	case choice.tailnet:
		return chosen.URL, nil
	case chosen.Domain == "":
		return "", fmt.Errorf("Pier has no .local name for %q; add local: to it in pier.yaml, or use --tailscale", chosen.Name)
	case choice.lan && chosen.LANURL == "":
		return "", fmt.Errorf("Pier has no LAN address for %s right now; run pier up, and check local.lan is not false", chosen.Name)
	case choice.lan:
		return chosen.LANURL, nil // works even when the .local name does not resolve
	case chosen.LocalURL == "":
		state := chosen.LocalState
		if state == "" {
			state = "down"
		}
		hint := "run pier up"
		if chosen.LANURL != "" {
			hint = "pier qr --lan works meanwhile"
		}
		return "", fmt.Errorf("Pier is not serving %s right now (%s); %s", chosen.Domain, state, hint)
	case choice.ca:
		return "http://" + chosen.Domain + localproxy.InstallPath, nil
	default:
		return chosen.LocalURL, nil
	}
}
