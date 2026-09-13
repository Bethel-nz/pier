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

v0.1 supports HTTP and HTTPS proxy targets only. Raw TCP is not configured.

## Listeners

- `public: false` → Tailscale Serve on HTTPS port `8443`
- `public: true` → Tailscale Funnel on HTTPS port `443`

Serve and Funnel never share a listener. Changing `public` (or using `pier share` / `pier unshare`) plans a delete on the old listener and a create on the new one.

## Discovery

`pier` walks from the current directory toward the filesystem root looking for `pier.yaml`. `--config` selects an explicit file. `pier init` writes a minimal config and a UUID in `.pier/id`. `.pier/` is gitignored.

## Runtime overrides

`pier share <service>` stores `public: true` in per-project state under the user config directory. `pier.yaml` is not modified. `pier unshare` removes the override. The override is persisted only after Tailscale verifies the route change.

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
