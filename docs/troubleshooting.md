# Safety and troubleshooting

## Ownership

After a successful `pier up`, Pier records the routes it created or updated in `<user-config-dir>/pier/projects/<project-id>.json`. `pier down` deletes only those owned routes. Routes created by hand or by another tool are left alone.

## Conflicts

If something already answers on the same route identity Pier would claim, and Pier does not own it, `pier plan` / `pier up` refuse with a conflict. The error shows current and desired values and suggests `pier up --force`.

`--force` takes over that identity. It still does not wipe unrelated routes on the machine.

## Why Pier never resets a whole backend

A full backend reset would wipe every handler on the machine, including routes Pier does not own. Pier only turns off and republishes the specific identities this project recorded.

## What `pier down` removes

- Routes whose identities were saved as owned by this project (local names this project declared, and any device/public handlers it applied)

## What `pier down` leaves untouched

- Unrelated routes from other tools or projects
- Backend authentication, DNS policy, and certificates that Pier did not create
- Local application processes
- `pier.yaml`

## Public routes

Anything public is reachable by anyone on the internet, so Pier keeps it in view:

- `pier status` reads what is live right now, not only saved state. A service shows a URL only when its route is live, and its PUBLIC column says how long it has been public (`PUBLIC 3h`). Lines starting `drift` say where live state differs from `pier.yaml`, each with the command that fixes it.
- `pier status --all` lists every route Pier can see on this machine, public ones first, with the Pier project that owns each one, or `-` for none.
- `pier up`, `pier status`, and `pier doctor` warn about public routes no Pier project owns, and about public routes that have been up for more than 24 hours.

## Doctor

`pier doctor` reports config health, whether optional exposure backends are available, local CA / daemon state, and whether local targets answer on TCP. Failures are diagnostic data. `--verbose` adds raw backend diagnostics. It also warns about public routes (above), and when the `pier` on your PATH is a different binary from the one running — the usual reason a `go install`ed fix seems to change nothing.

Common blocks for optional device/public paths:

- backend binary missing or daemon stopped
- signed-out or unauthorized client
- public exposure used without that path's authorization

When a backend documents its own listener ports or bandwidth limits, Pier follows those rules for that path. Local `.local` / LAN serving does not need those backends.

## Health and `--strict`

Pier dials each service host/port with a 500ms timeout. A failed check is shown as `unavailable` and does not, by itself, stop `pier up`. `--strict` turns any unavailable target into a pre-apply error.

## DNS and reachability

If `pier status` shows a URL but the browser fails, check that this machine and the local process are still online, and that the exposure path for that URL is still available. Propagation, certificate issuance, and bandwidth limits on a device/public backend are that backend's responsibility.

For `.local` and `lan` URLs, see below and the gotchas in the [README](../README.md#gotchas).

## `.local` names on other devices

Pier publishes each `.local` name through the system's mDNS responder (mDNSResponder on macOS, the DNS client on Windows) or, on Linux, its own. If a name works on this machine but not on another device:

1. Open `https://<this machine's LAN IP>/` from that device. Pier's "Unknown name" page means the device reaches Pier and only name lookup is failing. A hang means a firewall, or macOS Local Network permission, is blocking Pier.
2. On macOS, `dns-sd -G v4 myapp.local` should print this machine's LAN IP. If it does, Pier is publishing correctly.
3. Make sure IPv6 is on for the other device's network adapter. Many home routers drop IPv4 multicast between Wi-Fi clients but pass IPv6, so with IPv6 off the device never hears the answer. On Windows, run in an administrator shell: `Enable-NetAdapterBinding -Name "Wi-Fi" -ComponentID ms_tcpip6`, then `ipconfig /flushdns`.
4. If the router drops both, look for "AP isolation", "client isolation", or "multicast" settings on the router.
5. Meanwhile use the plain-HTTP `lan` address `pier up` prints, or scan `pier qr --lan`.
