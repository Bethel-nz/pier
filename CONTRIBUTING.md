# Contributing to Pier

Start with the [README](README.md) for usage and the [architecture guide](docs/architecture.md) for the package map. Bug reports, documentation fixes, tests, and focused code changes are welcome.

## Project scope

Pier is a proxy and orchestration layer that runs on your machine. It reads `pier.yaml`, reconciles the declared services onto the selected exposure paths, verifies what is live, and persists ownership so cleanup affects only this project's routes.

- Pier owns its local `.local` / LAN daemon, proxy, certificates, and name publishing.
- Tailscale and Cloudflare are optional backends driven through the installed `tailscale` and `cloudflared` CLIs. Pier does not reimplement their tunnel protocols. Missing backends do not prevent other available paths from working.
- Services can point at applications you start yourself, or use the optional `run:` / `watch:` process support in `internal/runner`.
- Keep business logic in the relevant packages and `internal/app`, with command parsing in `internal/cli` and output in `internal/render`. `pier plan` and `pier up` share the backend reconciliation path; keep-only plans must not mutate routes.

Useful starting points:

| Area | Packages |
| --- | --- |
| Config and project discovery | `internal/config`, `internal/project` |
| Route planning, apply/verify, and ownership | `internal/reconcile`, `internal/state` |
| Command use cases and output | `internal/app`, `internal/cli`, `internal/render` |
| External backend adapters | `internal/tailscale`, `internal/cloudflare` |
| Local serving and names | `internal/localname`, `internal/localproxy`, `internal/mdns`, `internal/dnssd` |
| Certificates and OS trust | `internal/certs`, `internal/trust` |
| Application processes | `internal/runner` |

## Development setup

Install Git and the Go version declared in [`go.mod`](go.mod), currently Go 1.25.0 or newer. Fork the repository if you need your own push destination, then clone your fork or this repository:

```bash
git clone https://github.com/Bethel-nz/pier.git
cd pier
git switch -c your-change
go mod download
go run ./cmd/pier --help
```

To install the checkout's CLI, run `go install ./cmd/pier`. The binary goes into `$(go env GOPATH)/bin`, or `GOBIN` if set; see the [README install instructions](README.md#install).

**Ordinary contributions and unit tests do not require an installed Tailscale CLI, a live Tailscale account, or a Cloudflare account.** Backend tests use fakes and fixtures; use temporary directories for test config and state instead of your real project or user configuration.

Live manual testing is optional. Install and authorize only the backends needed for the exposure path you are testing. For local names, trust-store changes and privileged ports have OS-specific requirements; follow the [README](README.md#local-names) on a disposable test machine.

## Focused checks

Start with the package you changed, then include related packages when a change crosses a boundary. For example:

```bash
go test ./internal/config
go test ./internal/tailscale ./internal/reconcile
```

Use `go test ./internal/<changed-package>` for another package, replacing the placeholder with its directory. Format only the Go files you changed; for example, `gofmt -w internal/config/validate.go`.

For broader verification, use the [README checks](README.md#verify):

```bash
go vet ./...
go test ./...
```

CI also checks formatting and runs platform-specific installer checks and tagged end-to-end tests. See [the CI workflow](.github/workflows/ci.yml) for the exact commands. For documentation-only changes, check links, commands, and any YAML you edit; say which checks you actually ran in the PR.

### Optional smoke tests

The smoke script needs Bash, Python 3, and Go. With `PIER_SMOKE_APPLY` unset, run:

```bash
./scripts/smoke.sh
```

It starts two loopback HTTP servers, writes a temporary `pier.yaml`, and runs `pier validate` and `pier plan` without changing live routes.

Only opt into live apply on a disposable machine authorized for the exposure paths under test. The script's current apply path exercises Tailscale routes and also needs the `tailscale` CLI, a connected test node with the required capabilities, and `curl`:

```bash
PIER_SMOKE_APPLY=1 ./scripts/smoke.sh
```

The script calls `pier down` during cleanup and checks that unrelated Tailscale routes are unchanged. This is optional validation, not a prerequisite for an ordinary contribution.

## Safety boundaries

- Preserve unrelated Tailscale and other backend routes. Remove only routes recorded as owned by the current Pier project; `--force` is an explicit takeover of a conflicting identity, not a general cleanup mechanism.
- Never use `tailscale serve reset` or `tailscale funnel reset` in implementation, tests, or cleanup instructions.
- Keep public exposure explicit: `public: true` and `pier share` make a service reachable from the internet. Review changes to exposure defaults, ownership, and cleanup carefully.
- Do not commit runtime state, local CA private keys, tunnel credentials, or real network configuration. Sanitize `pier.yaml`, command output, logs, and screenshots before sharing: remove secrets, credentials, private tailnet information, and sensitive network names. Use placeholders for private hostnames and addresses.

See [Safety](README.md#safety) and [Who sees your traffic](README.md#who-sees-your-traffic) for the user-facing guarantees.

## Choose an issue

Search [existing issues](https://github.com/Bethel-nz/pier/issues) and [open pull requests](https://github.com/Bethel-nz/pier/pulls) before starting to avoid duplicating work. The `good first issue` and `help wanted` labels are useful starting points. Read the issue's acceptance criteria and discussion; ask on the issue if the intended behavior is unclear. For a new feature, describe the use case and proposed behavior in a feature request before making a large change.

Use the bug form for reproduction steps, expected and actual behavior, OS, Pier version or commit, relevant backend versions, sanitized configuration, and validation already performed. A feature request should explain the current workflow, desired outcome, and alternatives considered.

## Pull requests

- Keep each PR scoped to one issue and link it in the PR description, using `Closes #<number>` when it fully resolves that issue.
- Explain the problem, what changed, and the resulting behavior. Update the relevant usage or configuration docs when behavior changes.
- Add or update focused tests for behavior changes, using the existing package patterns. Keep test data disposable and preserve the ownership and safety boundaries above.
- Report exact checks actually run and their results. List checks not run and why; do not mark a check as passed merely because it is recommended here. Include sanitized reproduction or manual validation steps where relevant.
- Call out changes to public exposure, backend commands, ownership, or cleanup so reviewers can assess them. Use the [PR template](.github/pull_request_template.md) as a checklist.

Do not push release tags unless the repository owner asks.
