# Architecture

Pier is a declarative, local-first proxy layer. You own the binary, the config, the certificates, and the routes. Pier reconciles what you declared in `pier.yaml` onto the exposure paths you chose, then tells you what is actually live.

```text
pier.yaml
  -> parse and validate
  -> normalized desired services and routes
  -> actual routes (Pier-owned local + optional device/public backends)
  -> reconciliation plan
  -> apply and verify
  -> persist ownership
  -> render through CLI or TUI
```

Local `.local` / LAN serving is Pier's own daemon. Other exposure paths are optional backends Pier drives when they are installed and available. Pier skips a path that is missing and still brings up the rest.

## Packages

| Package | Role |
| --- | --- |
| `internal/config` | Load, default, normalize, and validate `pier.yaml` |
| `internal/project` | Discover `pier.yaml` and initialize `.pier/id` |
| `internal/state` | Atomic per-project ownership and runtime overrides |
| `internal/reconcile` | Pure planner and ordered apply/verify for device/public routes |
| `internal/health` | Bounded TCP probe of local targets |
| `internal/app` | Shared use cases for CLI and TUI |
| `internal/cli`, `internal/render` | Cobra commands and text/JSON output |
| `internal/tui` | Bubble Tea UI over `app.Service` |
| `internal/platform` | `open` / `copy` OS commands |
| `internal/runner` | Optional `run:` / `watch:` process ownership |
| `internal/certs`, `internal/trust` | Local CA, project certs, OS trust |
| `internal/localname`, `internal/localproxy`, `internal/mdns` | `.local` names, HTTPS/HTTP proxy, mDNS, LAN ports, dashboard API |
| `internal/capture`, `internal/replay` | Request capture store and replay |
| `internal/tailscale` | Optional device/public backend adapter (CLI boundary, diagnostics, route JSON) |

Business logic does not live in Cobra handlers or Bubble Tea `View`. `pier plan` and `pier up` call the same reconcile path for routes Pier applies through a backend. Keep-only plans do not mutate anything.

## Identity

- **Local names** are identified by hostname (for example `myapp.local`). The daemon owns DNS answers, TLS, and the proxy to loopback.
- **Device/public HTTPS routes** (when a backend is in use) are identified by HTTPS listener plus path (`https:{port}:{path}`). Target and public are values. Service name is ownership metadata.
- **TCP** is identified by listen port on the paths you enabled.

## Ownership

After a successful apply, Pier records what it created or updated under the user config directory. `pier down` removes only those owned routes. Hand-edited or third-party routes stay unless you pass `--force` to take over a conflicting identity.

## Verification

```bash
go test ./...
gofmt -w .
go vet ./...
```

Live apply smoke (needs disposable backends for the paths you exercise):

```bash
PIER_SMOKE_APPLY=1 ./scripts/smoke.sh
```

Without `PIER_SMOKE_APPLY=1`, the script still validates and plans against disposable local servers and does not change live routes.

## Release

Do not push a release tag unless the repository owner asks.
