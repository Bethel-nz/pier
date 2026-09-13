# Architecture

Pier is a declarative project layer over the installed Tailscale CLI.

```text
pier.yaml
  -> parse and validate
  -> normalized desired routes
  -> actual Tailscale routes + Pier-owned routes
  -> reconciliation plan
  -> apply and verify
  -> persist ownership
  -> render through CLI or TUI
```

## Packages

| Package | Role |
| --- | --- |
| `internal/config` | Load, default, normalize, and validate `pier.yaml` |
| `internal/project` | Discover `pier.yaml` and initialize `.pier/id` |
| `internal/tailscale` | Process boundary, diagnostics, Serve/Funnel JSON, argv |
| `internal/state` | Atomic per-project ownership and runtime overrides |
| `internal/reconcile` | Pure planner and ordered apply/verify |
| `internal/health` | Bounded TCP probe |
| `internal/app` | Shared use cases for CLI and TUI |
| `internal/cli`, `internal/render` | Cobra commands and text/JSON output |
| `internal/platform` | `open` / `copy` OS commands |
| `internal/tui` | Bubble Tea UI over `app.Service` |

Business logic does not live in Cobra handlers or Bubble Tea `View`. `pier plan` and `pier up` call the same `reconcile.Build`. Keep-only plans do not mutate Tailscale.

## Identity

A Tailscale route is identified by HTTPS listener plus path (`https:{port}:{path}`). Target and `Public` are values. Service name is ownership metadata.

## Verification

```bash
go test ./...
gofmt -w .
go vet ./...
```

Live Tailscale smoke:

```bash
PIER_SMOKE_APPLY=1 ./scripts/smoke.sh
```

Without `PIER_SMOKE_APPLY=1`, the script still validates and plans against disposable local servers and does not change Tailscale routes.

## Release

v0.1 is tagged only after the tree is verified. Do not push a tag unless the repository owner asks.
