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
    local: greppa.local

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
| `services.<name>.public` | Optional per-service override of `defaults.public` |
| `services.<name>.protocol` | Optional per-service `http` or `https` |
| `services.<name>.local` | Optional `.local` name served over HTTPS on the local network |

v0.1 supports HTTP and HTTPS proxy targets only. Raw TCP is not configured.

## Examples

- [`basic.yaml`](../examples/configs/basic.yaml) shows the smallest private service configuration.
- [`multiple-services.yaml`](../examples/configs/multiple-services.yaml) shows path routing for multiple services and a per-service HTTPS upstream.
- [`public-webhook.yaml`](../examples/configs/public-webhook.yaml) keeps the main app private while exposing only a webhook through Funnel.
- [`local-domains.yaml`](../examples/configs/local-domains.yaml) serves `.local` HTTPS names on the LAN alongside private Tailscale URLs.
- [`bun-server`](../examples/bun-server/) is a runnable local demo and remains private by default.

## Listeners

- `public: false` → Tailscale Serve on HTTPS port `8443`
- `public: true` → Tailscale Funnel on HTTPS port `443`

Serve and Funnel never share a listener. Changing `public` (or using `pier share` / `pier unshare`) plans a delete on the old listener and a create on the new one.

## Local names

`local` must be a lowercase hostname ending in `.local`, such as `greppa.local` or `api.greppa.local`, and unique across every Pier project on the machine. It routes the whole host to the service's target: `path` applies only to the Tailscale URL.

`pier up` issues `.pier/certs/cert.pem` and `key.pem` for the project's local names, signed by a per-user CA in the user config directory (`pier/ca/`). The CA is name-constrained to `.local`. Apps may reuse the project certificate directly, for example as Vite's `server.https`.

A background daemon (`pier locald`, started by `pier up`) answers multicast DNS for the names, listens on port 443 (8443 when 443 is unavailable) and port 80 (redirects, and the CA at `/.pier/ca.pem`), and proxies to the loopback target. The upstream sees `Host` set to its target and `X-Forwarded-Host` set to the `.local` name. Dev servers that proxy to another Pier name must rewrite `Host` (`changeOrigin: true`); Pier stops forwarding loops with `508`.

`pier status` shows a local URL only while the daemon serves the name. Otherwise it shows the state: `probing`, `conflict` (another device or project answers for the name), `paused`, or `down`.

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
