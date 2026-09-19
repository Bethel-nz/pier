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
- protocol is http or https and describes the local upstream.
- public: false uses Tailscale Serve for tailnet-only access. public: true uses Tailscale Funnel and exposes the service to the internet. Keep it false unless the user explicitly requests public access.

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
