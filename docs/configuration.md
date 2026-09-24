# Configuration

Pier's committed project file is `pier.yaml`. Runtime state is never committed.

## Schema

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
    local: myapp.local

  api:
    target: localhost:4000
    path: /api
    protocol: http

  webhook:
    target: localhost:8787
    path: /hooks
    public: true
```

| Field | Meaning |
| --- | --- |
| `version` | Must be `1` |
| `name` | Project name |
| `defaults.public` | Inherited Funnel (`true`) vs Serve (`false`) |
| `defaults.protocol` | Inherited `http` or `https` |
| `services.<name>.target` | Host and port of the local process |
| `services.<name>.path` | URL path on the Tailscale listener |
| `services.<name>.public` | Optional per-service override of `defaults.public`: `true`, `false`, or how long to stay public after `pier up`, such as `2h` |
| `services.<name>.protocol` | Optional per-service `http` or `https` |
| `services.<name>.local` | Optional `.local` name served over HTTPS on the local network |
| `services.<name>.run` | Optional shell command `pier up` starts and keeps running |
| `services.<name>.dir` | Folder `run` starts in, relative to the project root |
| `services.<name>.env` | Extra environment variables for `run` |
| `services.<name>.watch` | Globs, relative to `dir`, whose changes restart `run` |
| `services.<name>.throttle` | Slow the service to `slow-3g`, `3g`, `4g`, or `{latency, down, up}` |
| `services.<name>.capture` | Keep the service's requests this long for `pier replay`, such as `2min`, `1hr`, `24h`, or `7d` |

v0.1 supports HTTP and HTTPS proxy targets only. Raw TCP is not configured.

## Examples

- [`basic.yaml`](../examples/configs/basic.yaml) shows the smallest private service configuration.
- [`multiple-services.yaml`](../examples/configs/multiple-services.yaml) shows path routing for multiple services and a per-service HTTPS upstream.
- [`public-webhook.yaml`](../examples/configs/public-webhook.yaml) keeps the main app private while exposing only a webhook through Funnel.
- [`local-domains.yaml`](../examples/configs/local-domains.yaml) serves `.local` HTTPS names on the LAN alongside private Tailscale URLs.
- [`bun-server`](../examples/bun-server/) is a runnable local demo and remains private by default.

## Running services

`run` is optional. Without it, Pier routes to whatever already listens on `target`. With it, `pier up` starts the commands and stays in the foreground like `docker compose up`, then:

1. Pier starts each command through the shell (`sh -c`, or `cmd /c` on Windows) in `dir`, with `PORT` set to the target's port unless `env` sets it.
2. It waits up to 60 seconds for every target to accept connections, then applies routes and prints the service table.
3. Output streams under each service's name. A command that exits is reported and not restarted, so a crash never scrolls its own error away.
4. Ctrl-C stops every command and everything it started. Routes stay until `pier down`, and meanwhile `.local` names show Pier's "not responding" page.

```yaml
services:
  api:
    target: localhost:8080
    run: go run ./cmd/api
    watch: ["**/*.go", "templates/*.html"]
  web:
    target: localhost:3000
    run: bun run dev
    dir: web
```

`watch` restarts the command when a matching file is added, changed, or removed. `**` spans any number of folders. `.git`, `node_modules`, and `.pier` are skipped unless a glob names them. Dev servers with their own hot reload (Vite, Next.js) don't need `watch`.

A second `pier up` in the same project updates routes without starting the commands again.

## Throttle and capture

```yaml
services:
  web:
    target: localhost:3000
    throttle: 3g                      # or slow-3g, 4g
  api:
    target: localhost:8080
    throttle: {latency: 300ms, down: 1.5mbit, up: 750kbit}
  hooks:
    target: localhost:8787
    path: /hooks
    public: true
    capture: 24h
```

| Preset | Latency | Down | Up |
| --- | --- | --- | --- |
| `slow-3g` | 2 s | 400 kbit/s | 400 kbit/s |
| `3g` | 560 ms | 1.6 Mbit/s | 750 kbit/s |
| `4g` | 170 ms | 9 Mbit/s | 1.5 Mbit/s |

Latency is added once per request. Bandwidth paces request and response bodies. WebSocket messages are not slowed.

`capture` keeps each request and its response: method, path, headers, and up to 1 MB of each body, in `.pier/capture.db` (SQLite, gitignored with `.pier/`). `Authorization`, `Cookie`, `Set-Cookie`, and `Proxy-Authorization` are stored as `[redacted]`. A pruning routine in Pier's background process deletes requests older than `capture` every minute, whether or not anything is being captured at that moment, and deletes all of a service's requests once its `capture` is removed. `capture` can be 1 minute to 30 days. `pier clean` deletes every project's capture file.

Lengths of time in `pier.yaml` (`capture`, `public`) take Go's forms (`90s`, `2m`, `1h30m`) or a number and a unit: `2min`, `1hr`, `24 hours`, `7d`, `3 days`, `2w`.

Both need Pier to see the traffic. Tailscale sends requests straight to the service, so a throttled or captured service gets a tap: a loopback port in Pier's background process that Tailscale Serve and Funnel point at instead. The tap applies the throttle and capture, then forwards to `target` with Tailscale's `X-Forwarded-*` headers intact. The service's `.local` name goes through the same throttle and capture. Tap ports are saved and reused, so repeated `pier up` runs don't move Tailscale routes.

### Replay

```sh
pier replay                   # newest captured requests
pier replay 42                # send #42 to its service again; prints 500 → 200 and the new body
pier replay 42 43             # several, in the order given
pier replay --since 10m       # everything from the last 10 minutes, oldest first
pier replay --service hooks   # only one service (with a list or --since)
pier replay 42 --show         # print the request and its answer instead of sending
```

A replay goes straight to the service's current `target`, not through Tailscale or its tap, so it is neither throttled nor captured again. It carries `X-Pier-Replay: <id>` and the original `X-Forwarded-Host`. Redacted headers are left out, so a request that needed `Authorization` has to be allowed some other way while you test. Webhook signatures such as `Stripe-Signature` are kept, though a provider that signs timestamps may reject a replay once its tolerance window passes.

## Listeners

- `public: false` → Tailscale Serve on HTTPS port `8443`
- `public: true` → Tailscale Funnel on HTTPS port `443`

Serve and Funnel never share a listener. Changing `public` (or using `pier share` / `pier unshare`) plans a delete on the old listener and a create on the new one.

### Public for a while

```yaml
services:
  demo:
    target: localhost:3000
    public: 2h
```

`public: 2h` makes the service public through Funnel for two hours from each `pier up`, then private again on the tailnet listener. `pier status` shows the time left, such as `true (1h12m left)`. Running `pier up` again starts a fresh two hours. The window is between 1 minute and 7 days, written like any length of time in `pier.yaml` (below).

Pier's background process closes the window when it ends, even with no terminal open. If Tailscale is unreachable at that moment, it retries every 30 seconds and says so in `pier doctor`. If this machine is asleep, the window closes as soon as it wakes. If the machine restarts before the window ends, Funnel stays on until Pier runs again: turn on `local.autostart`, or run any `pier up`.

`pier share` and `pier unshare` still override a timed service, with no time limit.

## Local names

`local` must be a lowercase hostname ending in `.local`, such as `myapp.local` or `api.myapp.local`, and unique across every Pier project on the machine. It routes the whole host to the service's target: `path` applies only to the Tailscale URL.

`pier up` issues `.pier/certs/cert.pem` and `key.pem` for the project's local names, signed by a per-user CA in the user config directory (`pier/ca/`). The CA is name-constrained to `.local`. Apps may reuse the project certificate directly, for example as Vite's `server.https`.

A background daemon (`pier locald`, started by `pier up`) answers multicast DNS for the names, listens on port 443 (8443 when 443 is unavailable) and port 80 (redirects, and the CA install page at `/.pier/` with the CA as `/.pier/ca.pem`, `/.pier/pier-local-ca.crt`, and an iOS profile), and proxies to the loopback target. The upstream sees `Host` set to its target and `X-Forwarded-Host` set to the `.local` name. For same-origin `GET` and `HEAD` requests, `Origin` is translated to the target too, so dev servers that guard their internals by origin (Next.js `/_next` and hot reload) work without `allowedDevOrigins`; other origins and state-changing requests pass through unchanged. Dev servers that proxy to another Pier name must rewrite `Host` (`changeOrigin: true`); Pier stops forwarding loops with `508`.

`pier status` shows a local URL only while the daemon serves the name. Otherwise it shows the state: `probing`, `conflict` (another device or project answers for the name), `paused`, or `down`.

### LAN fallback

Each local service also gets a plain-HTTP port on every address of this machine, starting at `4100`, saved per service so it stays the same across `pier up` runs. `pier up` and `pier status` print it as `lan  web  http://<LAN IP>:4100/`, and `pier qr --lan` shows it as a QR code. It proxies exactly like the `.local` name, with the same throttle and capture, and needs no DNS, multicast, or certificate, so it works where `.local` does not. Because it is plain HTTP, pages there are not a secure context. A firewall on this machine may ask once to allow Pier's incoming connections.

### `local` settings

An optional top-level `local` block changes how the project's names are served:

```yaml
local:
  lan: false          # default true
  autostart: true     # default false
  tls:
    cert: certs/dev.pem
    key: certs/dev-key.pem
```

| Field | Meaning |
| --- | --- |
| `local.lan` | `false` serves the names to this machine only: other devices get `403`, and there is no plain-HTTP `lan` address. They can still see the name, because hiding it would need `sudo`. |
| `local.autostart` | Serve the names after login without running `pier up`. Pier adds a per-user login item (a macOS LaunchAgent, a systemd user unit, or the Windows `Run` key) while any project asks for it, and removes it when none do. |
| `local.tls.cert`, `local.tls.key` | Serve your own certificate instead of Pier's, with paths relative to the project root. It must cover every `local` name in the project. Pier then skips its CA and the trust prompt. |

### Dashboard API

While the daemon runs, it serves a JSON API on `127.0.0.1` at a random port; `pier doctor` prints it as `api:`, and `--json` output includes it as `local.apiUrl`. It only answers requests whose `Host` is `127.0.0.1` or `localhost` on that port (so a DNS-rebinding page cannot use it), and every write must send an `X-Pier` header, which a cross-site page cannot send without a CORS preflight that the API never answers.

| Endpoint | Returns |
| --- | --- |
| `GET /api/status` | `{daemon, names, projects}`: the daemon (ports, warnings), every served name with its state, and every project with its names, live URLs, settings, and paused services |
| `GET /api/requests?host=&limit=` | Recent proxied requests, newest first (`time, host, method, path, status, durationMs, bytes, client, userAgent`); the daemon keeps the last 500 in memory |
| `GET /api/events` | Server-sent events: `status` (same shape as `/api/status`) whenever names, projects, or warnings change, and `request` for every proxied request |
| `POST /api/projects/{id}/services/{service}/pause` and `.../resume` | Runs `pier pause` or `pier resume` for that project and returns its `--json` output |

## Discovery

`pier` walks from the current directory toward the filesystem root looking for `pier.yaml`. `--config` selects an explicit file. `pier init` writes a minimal config and a UUID in `.pier/id`. `.pier/` is gitignored.

## Runtime overrides

`pier share <service>` stores `public: true` in per-project state under the user config directory. `pier.yaml` is not modified. `pier unshare` removes the override. The override is persisted only after Tailscale verifies the route change.

`pier pause <service>` stores a runtime pause flag in the same per-project state and deletes that service's Tailscale route. The local process is not started, stopped, or signaled. `pier resume` clears the flag and restores the route. `pier up` skips paused services. `pier service add` writes a new service into `pier.yaml` and does not change Tailscale until `pier up` or `pier resume`.

## Machine-readable output

`--json` wraps every command in:

```json
{
  "version": 1,
  "command": "status",
  "project": { "id": "...", "path": "..." },
  "data": {},
  "warnings": [],
  "errors": []
}
```
