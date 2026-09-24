package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const agentsPrompt = `## Pier

Configure this repository with Pier, an installed CLI that maps local services to Tailscale Serve or Funnel URLs. Do not assume access to Pier's source repository.

Inspect files in the current repository, such as package.json scripts, compose.yaml, Procfile, README files, and environment examples, to identify each HTTP or HTTPS service and its local port. Ask the user when a port or service is ambiguous.

Pier uses a pier.yaml file in the repository root:

` + "```yaml" + `
version: 1
name: project-name

defaults:
  public: false
  protocol: http

services:
  web:
    target: localhost:3000
    path: /
` + "```" + `

- Service names must start with a lowercase letter and contain only lowercase letters, numbers, or hyphens.
- target must use localhost or another loopback address with a port.
- path must begin with /, be path-clean, and be unique across services.
- protocol is http or https and describes the local upstream. protocol: tcp forwards raw TCP for databases and other non-HTTP servers, privately only; listen sets the port clients use (default: the target's port), and public, path, throttle, and capture do not apply.
- public: false uses Tailscale Serve for tailnet-only access. public: true uses Tailscale Funnel and exposes the service to the internet. Keep it false unless the user explicitly requests public access. A duration such as public: 2h is public for that long after each pier up, then private again; prefer it when the user needs a public link only for a demo or webhook test.
- local is optional and must end in .local, such as web.project-name.local. pier up then serves it over HTTPS to every device on the same network, with a certificate Pier issues. Add it only when the user wants LAN or phone access.
- provider is optional: tailscale (the default) or cloudflare. provider: cloudflare serves the service publicly at <service>.<domain> through a Cloudflare Tunnel, where domain: example.com is set once at the top of pier.yaml; hostname: api-v2 overrides the name. pier up drives cloudflared (the user installs it and logs in once in the browser). Use it only when the user asks for their own domain; it is public on the internet. A cloudflare service is not on Tailscale: do not give it public or path.
- run is optional: the command that starts the service, such as "bun run dev". pier up then starts it with PORT set to the target port and streams its output until Ctrl-C. dir sets its folder, env adds variables, and watch lists globs, such as "**/*.go", whose changes restart it. Add run only when the user wants Pier to start their apps; skip watch for dev servers with their own hot reload.
- throttle is optional and slows a service to test slow networks: slow-3g, 3g, 4g, or {latency: 300ms, down: 1mbit, up: 500kbit}.
- capture is optional, such as 24h: Pier keeps the service's requests that long so pier replay <id> can send them again. Suggest it for webhook endpoints.

Reference configurations: https://github.com/Bethel-nz/pier/tree/main/examples/configs

Workflow:

1. Run pier --help and pier service add --help for the installed command reference.
2. If pier.yaml is missing, run pier init using the repository name, then replace its sample service with the repository's actual services.
3. Edit pier.yaml directly or add services with pier service add <name> --target <host:port> --path <path> --protocol <http|https>. Add --public only with explicit user approval.
4. Run pier validate.
5. Run pier plan and explain validation errors or unmanaged-route conflicts before changing routes.
6. Run pier up only when the user wants the routes applied and Tailscale is installed, running, and signed in.

Pier manages Tailscale routes only. It does not start or stop application processes. Never use tailscale serve reset or tailscale funnel reset; pier down removes only routes owned by this project.
`

func newAgentsCommand(rt *runtime) *cobra.Command {
	var write bool
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Print or write instructions for agents setting up Pier",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if !write {
				_, err := fmt.Fprint(rt.stdout, agentsPrompt)
				return err
			}

			path, err := filepath.Abs("AGENTS.md")
			if err != nil {
				return rt.renderer("agents").Error(fmt.Errorf("Pier could not resolve AGENTS.md: %w", err))
			}
			if err := writeAgentsFile(path); err != nil {
				return rt.renderer("agents").Error(err)
			}
			fmt.Fprintf(rt.stdout, "wrote %s\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "write the prompt to AGENTS.md")
	return cmd
}

func writeAgentsFile(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("Pier refused to overwrite existing %s", path)
	}
	if err != nil {
		return fmt.Errorf("Pier could not create %s: %w", path, err)
	}
	if _, err := io.WriteString(file, agentsPrompt); err != nil {
		_ = file.Close()
		return fmt.Errorf("Pier could not write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("Pier could not close %s: %w", path, err)
	}
	return nil
}
