# Pier

Describe the local services in a project, run `pier up`, and get stable Tailscale URLs for all of them.

Pier is a local Go CLI and TUI over the Tailscale CLI. It reads `pier.yaml`, reconciles those services onto Tailscale Serve (tailnet) and Funnel (public), and prints the URLs. It does not start your apps, implement a tunnel, or reset unrelated Tailscale configuration.

URL availability depends on the developer machine, the local service, and Tailscale remaining online.

## Prerequisites

- Go 1.25+ to build from source
- Tailscale installed, running, and signed in
- [MagicDNS](https://tailscale.com/docs/features/magicdns) and [HTTPS certificates](https://tailscale.com/docs/features/https) enabled for Serve
- [Funnel](https://tailscale.com/docs/features/tailscale-funnel) authorization on the device if any service uses `public: true`

See [Tailscale Serve](https://tailscale.com/docs/features/tailscale-serve) and [Tailscale Funnel](https://tailscale.com/docs/features/tailscale-funnel) for current platform requirements.

## Install

macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/Bethel-nz/pier/main/scripts/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/Bethel-nz/pier/main/scripts/install.ps1 | iex
```

The installers download the latest GitHub release, verify its SHA-256 checksum, and install Pier into a user-owned binary directory. Use `PIER_VERSION` to install a specific release or `PIER_INSTALL_DIR` to choose another destination.

From anywhere, with Go:

```bash
go install github.com/Bethel-nz/pier/cmd/pier@latest
```

From a source checkout:

```bash
go install ./cmd/pier
```

Either command puts `pier` in `$(go env GOPATH)/bin` (or `GOBIN` if set). Add that directory to your `PATH`.

## Quick start

```bash
pier init
pier validate
pier plan
pier up
pier status
pier
pier down
```

`pier` with no arguments opens the TUI when stdin and stdout are terminals. Otherwise it prints help. `pier tui` is the explicit alias.

## Example `pier.yaml`

```yaml
version: 1
name: greppa

defaults:
  public: false
  protocol: http

services:
  web:
    target: localhost:3000
    path: /

  api:
    target: localhost:4000
    path: /api

  webhook:
    target: localhost:8787
    path: /hooks
    public: true
```

`public: false` maps to Serve on HTTPS listener `8443`. `public: true` maps to Funnel on HTTPS listener `443`. Serve and Funnel never share a listener.

A service can also set `domain` to a name ending in `.local`, such as `my-app.local`. `pier up` publishes that name on the local network at the service's own port. Pier watches this machine's address and replaces the advertisement when Wi-Fi renumbers, and `pier down` withdraws it. The name is not a stored IP. macOS publishes through Bonjour (`dns-sd`). Linux publishes through Avahi. Windows publishes through the system DNS-SD API. The Tailscale URL is unchanged.

A longer copy lives in [`pier.example.yaml`](pier.example.yaml). Configuration details are in [`docs/configuration.md`](docs/configuration.md).

## Configuration examples

- [`examples/configs/basic.yaml`](examples/configs/basic.yaml) — one private HTTP service.
- [`examples/configs/multiple-services.yaml`](examples/configs/multiple-services.yaml) — several private services on separate paths, including an HTTPS upstream.
- [`examples/configs/public-webhook.yaml`](examples/configs/public-webhook.yaml) — a private app with one public Funnel webhook.
- [`examples/bun-server/`](examples/bun-server/) — a runnable Bun server demo with its own `pier.yaml`.

## CLI

| Command | What it does |
| --- | --- |
| `pier agents [--write]` | Print an agent-ready Pier setup prompt, or write it to `AGENTS.md` |
| `pier init [name]` | Create a minimal `pier.yaml` and `.pier/id` |
| `pier validate` | Schema and semantic checks without requiring services to run |
| `pier plan` | Print the non-mutating desired-versus-actual plan |
| `pier up` | Validate, check Tailscale, plan, apply, verify, persist ownership, print URLs |
| `pier down` | Remove only routes owned by this Pier project |
| `pier status` | Show services, health, public access, and URLs |
| `pier doctor` | Diagnose config, Tailscale, and local targets |
| `pier share <service>` | Runtime-only `public: true` override |
| `pier unshare <service>` | Remove the override and restore configured access |
| `pier pause <service>` | Remove that service's Tailscale route; the local process stays running |
| `pier resume <service>` | Restore a paused service route |
| `pier service add [name]` | Add a service to `pier.yaml` (Huh form in a TTY, or `--target`) |
| `pier open <service>` | Open the current Tailscale URL |
| `pier copy <service>` | Copy the current Tailscale URL |
| `pier` / `pier tui` | Interactive management interface |

Global flags: `--config`, `--json`, `--verbose`, `--no-color`.

- `pier up --strict` refuses to apply if a local target is unavailable. Without `--strict`, unavailable targets are reported and still configured.
- `pier up --force` and `pier plan --force` take over unmanaged Tailscale routes on the same listener and path.
- `--json` is supported for `validate`, `plan`, `status`, and `doctor` (and the other commands). The envelope is `{version, command, project, data, warnings, errors}` with schema version `1`.
- `--verbose` includes raw Tailscale stderr. Human-readable errors always lead with a Pier explanation.

## TUI keys

```
↑/↓ select   a add   space pause/resume
u up         d down  p plan   s share/unshare
c copy       o open  r refresh  ? help   q quit
```

Deletes and unmanaged-route takeovers ask for confirmation. `q` does not quit while a confirmation modal is open.

## Public exposure

`public: true` and `pier share` publish a path on the internet through Funnel. Anyone who can reach the Funnel URL can reach that local service. Tailscale still owns TLS, DNS, and Funnel policy; Pier only asks Tailscale to configure the path.

## Safety

Pier never runs `tailscale serve reset` or `tailscale funnel reset`. `pier down` removes only routes this project recorded as owned. Unrelated Tailscale routes are left untouched unless you pass `--force` to take over a conflicting path. Details: [`docs/troubleshooting.md`](docs/troubleshooting.md).

## Remove Pier routes

```bash
pier down
```

Then delete `pier.yaml` and `.pier/` if you no longer want the project. Runtime ownership state lives under the OS user config directory (`<user-config-dir>/pier/projects/<project-id>.json`) and is not committed.

## Verify

CI runs `gofmt`, `go vet ./...`, and `go test ./...`. Tailscale is not required in CI.

Local checks:

```bash
gofmt -w .
go vet ./...
go test ./...
./scripts/smoke.sh
```

`./scripts/smoke.sh` starts two loopback HTTP servers, writes a temporary `pier.yaml`, and runs `pier validate` and `pier plan`. It does not change Tailscale routes unless you set `PIER_SMOKE_APPLY=1`.

```bash
PIER_SMOKE_APPLY=1 ./scripts/smoke.sh
```

That apply path needs a disposable Tailscale node authorized for Serve (and Funnel if you add public routes). `pier down` runs in a trap and the script checks that unrelated Tailscale routes are unchanged.

Do not tag `v0.1.0` until that live run and a TUI pass have been done on a real node.

## License

Pier is available under the [MIT License](LICENSE).
