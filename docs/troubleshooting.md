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

## Public routes

Anything public is reachable by anyone on the internet, so Pier keeps it in view:

- `pier status` reads the routes Tailscale serves right now, not saved state. A service shows a URL only when its route is live, and its PUBLIC column says how long it has been public (`PUBLIC 3h`). Lines starting `drift` say where Tailscale differs from `pier.yaml`, such as an older public route an earlier run left behind, each with the command that fixes it.
- `pier status --all` lists every Serve and Funnel route on this machine, public ones first, with the Pier project that owns each one, or `-` for none.
- `pier up`, `pier status`, and `pier doctor` warn about public routes no Pier project owns, and about public routes that have been up for more than 24 hours.

## Doctor

`pier doctor` reports whether Tailscale is installed, the daemon is running, the client is signed in, MagicDNS/HTTPS/Funnel look available, and local targets answer on TCP. Failures are diagnostic data. `--verbose` adds raw Tailscale stderr. It also warns about public routes (above), and when the `pier` on your PATH is a different binary from the one running, the usual reason a `go install`ed fix seems to change nothing.

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

## `.local` names on other devices

Pier publishes each `.local` name through the system's mDNS responder (mDNSResponder on macOS, the DNS client on Windows) or, on Linux, its own. If a name works on this machine but not on another device:

1. Open `https://<this machine's LAN IP>/` from that device. Pier's "Unknown name" page means the device reaches Pier and only name lookup is failing. A hang means a firewall, or macOS Local Network permission, is blocking Pier.
2. On macOS, `dns-sd -G v4 myapp.local` should print this machine's LAN IP. If it does, Pier is publishing correctly.
3. Make sure IPv6 is on for the other device's network adapter. Many home routers drop IPv4 multicast between Wi-Fi clients but pass IPv6, so with IPv6 off the device never hears the answer. On Windows, run in an administrator shell: `Enable-NetAdapterBinding -Name "Wi-Fi" -ComponentID ms_tcpip6`, then `ipconfig /flushdns`.
4. If the router drops both, look for "AP isolation", "client isolation", or "multicast" settings on the router.
