# Safety and troubleshooting

## Ownership

After a successful `pier up`, Pier records the routes it created or updated in `<user-config-dir>/pier/projects/<project-id>.json`. `pier down` deletes only those owned routes. Routes created by hand or by another tool are left alone.

If `pier up` fails partway, Pier still records the routes it changed before the failure, as long as Tailscale shows them exactly as planned, so `pier down` can remove them. Routes it deleted before the failure stop being owned once Tailscale confirms they are gone.

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

## Background checks

After `pier up`, Pier's background process keeps checking every project that owns Tailscale routes or serves a Cloudflare Tunnel, every 30 seconds:

- If a route this project owns went missing or changed (Tailscale restarted, `tailscale serve reset` ran elsewhere, the node re-authenticated), Pier puts it back, through the same plan `pier up` uses. It never touches a route the project does not own and never deletes one. If `pier.yaml` changed since the last `pier up`, it leaves routes alone and says so: run `pier up` to apply the edit. A command changing the project's routes holds a lock until it has saved, so the check never puts back a route that `pier unshare`, `pier pause`, or `pier down` is removing. To take a route down for good, use those commands: one removed by hand with `tailscale serve ... off` comes back, because the project still owns it.
- It sends a `HEAD` request to each Serve, Funnel, and Cloudflare URL, end to end. A missing answer or a 5xx counts as failing; any other status means the path works. These requests carry an `X-Pier-Probe` token that only the running daemon knows, so they are never captured, and the proxy removes it before the request reaches your app. A visitor sending the header is captured as usual.

`pier status` shows `failing  web: failing since 14:02 (answered 502 Bad Gateway)` and `healed  web: ... put it back 2m ago`, and `pier status --json` has `verifiedAt`, `verifyError`, `failingSince`, and `repairedAt` per service. When Tailscale is down, the check waits longer each time, up to 5 minutes, and `pier status` shows one warning instead of retrying every second.

## Doctor

`pier doctor` reports config health, whether optional exposure backends are available, local CA / daemon state, and whether local targets answer on TCP. Failures are diagnostic data. `--verbose` adds raw backend diagnostics. It also warns about public routes (above), and when the `pier` on your PATH is a different binary from the one running — the usual reason a `go install`ed fix seems to change nothing.

Common blocks for optional device/public paths:

- backend binary missing or daemon stopped. For Tailscale, Pier needs a `tailscale` command on your `PATH` that belongs to the Tailscale you run; [Tailscale CLI](https://tailscale.com/kb/1080/cli) shows how to turn it on per platform. `pier doctor` warns when the `tailscale` on your `PATH` is a different version from the running Tailscale, such as an old Homebrew CLI next to the app
- signed-out or unauthorized client
- public exposure used without that path's authorization

When a backend documents its own listener ports or bandwidth limits, Pier follows those rules for that path. Local `.local` / LAN serving does not need those backends.

## Health and `--strict`

Pier dials each service host/port with a 500ms timeout. A failed check is shown as `unavailable`. With a [`health`](configuration.md#health) path, a target that accepts connections but answers its health path with an error is `unhealthy`, with the code or error. Neither, by itself, stops `pier up`. `--strict` turns any target that is not healthy into a pre-apply error.

A `run:` command that keeps crashing is restarted with backoff, then left down after 5 crashes in a minute. `pier status` shows `crashed (exit 1): <last line>`, and `pier status --json` has its last 20 lines under `process.output`.

## DNS and reachability

If `pier status` shows a URL but the browser fails, check that this machine and the local process are still online, and that the exposure path for that URL is still available. Propagation, certificate issuance, and bandwidth limits on a device/public backend are that backend's responsibility.

For `.local` and `lan` URLs, see below and the gotchas in the [README](../README.md#gotchas).

## `.local` names on other devices

Pier publishes each `.local` name through the system's mDNS responder (mDNSResponder on macOS, the DNS client on Windows) or, on Linux, its own. If a name works on this machine but not on another device:

1. Open `https://<this machine's LAN IP>/` from that device. Pier's "Unknown name" page means the device reaches Pier and only name lookup is failing. A hang means a firewall, or macOS Local Network permission, is blocking Pier.
2. On macOS, `dns-sd -G v4 myapp.local` should print this machine's LAN IP, once for each network it is on. If it does, Pier is publishing correctly.
3. Make sure IPv6 is on for the other device's network adapter. Many home routers drop IPv4 multicast between Wi-Fi clients but pass IPv6, so with IPv6 off the device never hears the answer. On Windows, run in an administrator shell: `Enable-NetAdapterBinding -Name "Wi-Fi" -ComponentID ms_tcpip6`, then `ipconfig /flushdns`.
4. If the router drops both, look for "AP isolation", "client isolation", or "multicast" settings on the router.
5. Meanwhile use the plain-HTTP `lan` address `pier up` prints, or scan `pier qr --lan`.
