# Safety and troubleshooting

## Ownership

After a successful `pier up`, Pier records the routes it created or updated in `<user-config-dir>/pier/projects/<project-id>.json`. `pier down` deletes only those owned routes. Routes created by hand or by another tool are left alone.

## Conflicts

If Tailscale already has a different handler on the same HTTPS listener and path, and Pier does not own it, `pier plan` / `pier up` refuse with a conflict. The error shows current and desired values and suggests `pier up --force`.

`--force` takes over that identity. It still does not run a Tailscale reset.

## Why Pier never resets Tailscale

`tailscale serve reset` and `tailscale funnel reset` would wipe every Serve/Funnel handler on the machine, including routes Pier does not own. Pier only emits path-specific `off` and path-specific Serve/Funnel publishes.

## What `pier down` removes

- HTTPS proxy handlers whose listener+path were saved as owned by this project

## What `pier down` leaves untouched

- Unrelated Serve/Funnel routes
- Tailscale authentication, MagicDNS, HTTPS certificates, and ACLs
- Local application processes
- `pier.yaml`

## Doctor

`pier doctor` reports whether Tailscale is installed, the daemon is running, the client is signed in, MagicDNS/HTTPS/Funnel look available, and local targets answer on TCP. Failures are diagnostic data. `--verbose` adds raw Tailscale stderr.

Common blocks:

- missing `tailscale` binary
- stopped daemon
- signed-out client
- Funnel used without Funnel authorization

See [Tailscale Serve](https://tailscale.com/docs/features/tailscale-serve) and [Tailscale Funnel](https://tailscale.com/docs/features/tailscale-funnel) for current requirements. Funnel's allowed public listener ports are documented there; Pier uses `443` for public routes and `8443` for tailnet-only routes.

## Health and `--strict`

Pier dials each service host/port with a 500ms timeout. A failed check is shown as `unavailable` and does not, by itself, stop `pier up`. `--strict` turns any unavailable target into a pre-apply error.

## DNS and reachability

Serve and Funnel URLs are Tailscale MagicDNS names. Propagation, certificate issuance, and Funnel bandwidth limits are Tailscale's. If `pier status` shows a URL but the browser fails, check that this machine, the local process, and Tailscale are still online.
