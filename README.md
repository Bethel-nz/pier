<p align="center"><img src="docs/pier.svg" width="96" alt=""></p>

# Pier

A privacy-first proxy you own.

Describe the local services in a project, run `pier up`, and get stable URLs — on your LAN, your devices, or the public internet. Pier runs on your machine, reads `pier.yaml`, and puts those services on the exposure path you chose. No SaaS tunnel owns the route. No third party sits in the middle of your traffic.

You own the infra: the binary, the config, the certificates, the URLs. Pier just reconciles what you declared and tells you the truth about what is live.

## Prerequisites

- Go 1.25+ to build from source
- Nothing else for `local:` names and LAN URLs — Pier serves those on your machine
- For other exposure paths, install whatever that path needs. Pier skips a path that is not available and still brings up the rest

## Install

macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/Bethel-nz/pier/main/scripts/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/Bethel-nz/pier/main/scripts/install.ps1 | iex
```

The installers download the latest GitHub release, verify its SHA-256 checksum, and install Pier into a user-owned binary directory. Use `PIER_VERSION` to install a specific release or `PIER_INSTALL_DIR` to choose another destination.

From anywhere, with Go:

```bash
go install github.com/Bethel-nz/pier/cmd/pier@latest
```

From a source checkout:

```bash
go install ./cmd/pier
```

Either command puts `pier` in `$(go env GOPATH)/bin` (or `GOBIN` if set). Add that directory to your `PATH`.

## Quick start

```bash
pier init
pier validate
pier plan
pier up
pier status
pier
pier down
```

`pier` with no arguments opens the TUI when stdin and stdout are terminals. Otherwise it prints help. `pier tui` is the explicit alias.

## Example `pier.yaml`

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

  webhook:
    target: localhost:8787
    path: /hooks
    public: true
```

By default services stay private. Set `public: true` (or a duration like `2h`) when a path should be reachable on the public internet — useful for webhooks and short demos. See [Public exposure](#public-exposure).

Databases and other non-HTTP servers use `protocol: tcp`. Pier forwards the raw connection on the paths you enabled, so `psql -h db.myapp.local` just works. See [TCP services](docs/configuration.md#tcp-services).

## Local names

Give a service a `local` name ending in `.local`, and `pier up` serves it over HTTPS to this machine and every phone, tablet, and laptop on the same network:

```yaml
services:
  web:
    target: localhost:3000
    local: myapp.local
```

```
local  web  https://myapp.local/
```

Nothing else to install. On `pier up`, Pier:

- issues a certificate into `.pier/certs/` from a local CA that can only sign `.local` names (and adds `.pier/` to `.gitignore`),
- asks the OS once to trust that CA (the macOS password dialog, a Windows confirmation, or `sudo` on Linux),
- publishes the names through the system's mDNS responder (Pier's own on Linux) and starts a small background daemon that proxies HTTPS on port 443 to your service, which stays bound to loopback.

The daemon answers with this machine's address on the asking device's own network, so a Wi-Fi change needs nothing from you. `pier pause` withdraws one name, `pier down` withdraws the project's names, and the daemon exits once no project declares a local name. Other exposure paths for that service are unchanged.

`.local` names need the network to pass multicast and each device to trust Pier's CA, and some networks and devices won't cooperate. So every local service also gets a plain-HTTP address on this machine's LAN IP, which `pier up` prints on a `lan` line, such as `http://192.168.1.162:4100/`. It needs nothing on the other device and works on any network that lets devices reach each other. The port stays the same across runs; the IP is whatever this machine has on its current network. `pier qr --lan` shows it as a QR code. It is plain HTTP, so browser features limited to secure pages (service workers, camera, clipboard) need the `.local` name or another HTTPS path instead.

### Trust HTTPS on other devices

`pier trust` covers only this machine. Every other device trusts Pier's CA once, and that covers every `.local` name Pier serves, including ones you add later. Redo it only after resetting the device or running `pier clean`, which replaces the CA.

On the device, open `http://<local-name>/.pier/` (or scan `pier qr --ca`). The page detects the device and offers a one-tap installer. Compare the fingerprint it shows with `pier doctor`. Then finish per device:

- **iPhone and iPad.** Open the page in Safari and allow the profile download. Install it in Settings → General → VPN & Device Management. Then turn on **Pier Local CA** in Settings → General → About → Certificate Trust Settings. iOS does not trust an installed root for HTTPS until you do this last step.
- **Android.** Install the downloaded file in Settings → Security → Encryption & credentials → Install a certificate → CA certificate. Browsers trust it; apps don't (see Gotchas).
- **Windows.** Open the downloaded file, choose Install Certificate → Local Machine → Trusted Root Certification Authorities.
- **Another Mac.** Open the downloaded file, then in Keychain Access set **Pier Local CA** to Always Trust.
- **iOS Simulator.** It has its own trust store, separate from the Mac's. Run `xcrun simctl keychain booted add-root-cert "$HOME/Library/Application Support/pier/ca/ca.pem"` once per simulator, and again after erasing it.
- **Android emulator.** Same as Android: drag `ca.pem` onto the emulator window, then install it as a CA certificate.

If the page won't load on the device, copy the CA over yourself: it is `~/Library/Application Support/pier/ca/ca.pem` on macOS (`pier doctor` prints the path on every OS). AirDrop, email, or USB all work; then follow the same steps.

Apps built on the system network stack, such as React Native and Expo apps on iOS, use the same trust, so point them at `https://<local-name>` and `wss://<local-name>` once the device trusts the CA. The full walkthrough, including WebSockets and switching networks, is in [`docs/mobile-apps.md`](docs/mobile-apps.md).

On Linux, binding port 443 needs `sudo setcap cap_net_bind_service=+ep "$(command -v pier)"`. Without it, Pier uses port 8443 and says so. Chrome and Firefox on Linux read their own certificate stores; Pier adds its CA there when NSS's `certutil` is installed.

A `local` name makes that service reachable by anyone on the same network. Use it on networks you trust.

A longer copy lives in [`pier.example.yaml`](pier.example.yaml). Configuration details are in [`docs/configuration.md`](docs/configuration.md).

### Gotchas

Hit in real testing. Details in [`docs/troubleshooting.md`](docs/troubleshooting.md).

- **Another device can't resolve `.local`.** Use the `lan` address `pier up` prints meanwhile. To fix the name, turn IPv6 on for both that device and the machine running Pier. Many home routers drop IPv4 multicast between Wi-Fi clients but pass IPv6.
  - Windows: `Enable-NetAdapterBinding -Name "Wi-Fi" -ComponentID ms_tcpip6`, then `ipconfig /flushdns`.
  - macOS: System Settings → Wi-Fi → Details → TCP/IP → Configure IPv6: Automatically.
  - Linux: `sysctl net.ipv6.conf.all.disable_ipv6` should print `0`.
  - Phones: on by default.
- **Windows network set to Public.** The firewall drops incoming mDNS replies. Run `Set-NetConnectionProfile -InterfaceAlias "Wi-Fi" -NetworkCategory Private`.
- **`ping <mac-name>.local` works but the Pier name doesn't.** Windows resolved the Mac's name over NetBIOS, not mDNS, so it proves nothing. Test with `ping myapp.local`.
- **Chrome on iPhone can't resolve `.local`, Safari can.** Use Safari, or turn off Secure DNS in Chrome's settings.
- **Certificate warning on phones and other machines, or an app error like "The certificate for this server is invalid".** The device doesn't trust Pier's CA yet. Follow [Trust HTTPS on other devices](#trust-https-on-other-devices). On iPhone, the step people miss is Certificate Trust Settings.
- **Android apps reject the certificate even after installing it.** Android apps ignore user-installed CAs by default. For a debug build, add a network security config that trusts user certificates, or use the `lan` address.
- **The URL has `:8443`.** Something else already holds port 443 on this machine, so Pier falls back to 8443 (or shares 443 on LAN addresses when it can). Run `pier doctor` if that is unexpected.
- **`pier: command not found` after `go install`.** Add `$(go env GOPATH)/bin` to your `PATH`.
- **Changes don't take effect in the daemon.** It keeps the environment it started with. After changing permissions or reinstalling, run `pier down && pier up` from the terminal you normally use.

## Configuration examples

- [`examples/configs/basic.yaml`](examples/configs/basic.yaml) — one private HTTP service.
- [`examples/configs/multiple-services.yaml`](examples/configs/multiple-services.yaml) — several private services on separate paths, including an HTTPS upstream.
- [`examples/configs/public-webhook.yaml`](examples/configs/public-webhook.yaml) — a private app with one public webhook path.
- [`examples/configs/local-domains.yaml`](examples/configs/local-domains.yaml) — `.local` HTTPS names for devices on the same network.
- [`examples/bun-server/`](examples/bun-server/) — a runnable Bun server demo with its own `pier.yaml`.

## CLI

| Command | What it does |
| --- | --- |
| `pier agents [--write]` | Print an agent-ready Pier setup prompt, or write it to `AGENTS.md` |
| `pier init [name]` | Create a minimal `pier.yaml` and `.pier/id` |
| `pier validate` | Schema and semantic checks without requiring services to run |
| `pier plan` | Print the non-mutating desired-versus-actual plan |
| `pier up` | Start `run:` commands, then validate, plan, apply, verify, persist ownership, print URLs |
| `pier down` | Remove only routes owned by this Pier project |
| `pier status [--all]` | Show what is actually live for each service, how long anything has been PUBLIC, and what differs from `pier.yaml`; `--all` lists every route Pier can see on this machine |
| `pier doctor` | Diagnose config, exposure paths, and local targets |
| `pier share <service>` | Runtime-only `public: true` override |
| `pier unshare <service>` | Remove the override and restore configured access |
| `pier pause <service>` | Withdraw that service's routes; the local process stays running |
| `pier resume <service>` | Restore a paused service |
| `pier service add [name]` | Add a service to `pier.yaml` (Huh form in a TTY, or `--target`) |
| `pier open <service> [--local]` | Open the current URL, or the `.local` URL |
| `pier copy <service> [--local]` | Copy the current URL, or the `.local` URL |
| `pier trust [--remove]` | Trust Pier's local CA again, or remove it (`pier up` trusts it for you) |
| `pier replay [id...]` | List requests kept by `capture:`, or send them to the service again (`--since 10m`, `--show`) |
| `pier` / `pier tui` | Interactive management interface |

Global flags: `--config`, `--json`, `--verbose`, `--no-color`.

- `pier up --strict` refuses to apply if a local target is unavailable. Without `--strict`, unavailable targets are reported and still configured.
- `pier up --force` and `pier plan --force` take over unmanaged routes on the same identity when Pier would otherwise refuse.
- `--json` is supported for `validate`, `plan`, `status`, and `doctor` (and the other commands). The envelope is `{version, command, project, data, warnings, errors}` with schema version `1`.
- `--verbose` includes raw backend diagnostics. Human-readable errors always lead with a Pier explanation.

## TUI keys

```
↑/↓ select   a add   space pause/resume
u up         d down  p plan   s share/unshare
c copy       o open  r refresh  ? help   q quit
```

Deletes and unmanaged-route takeovers ask for confirmation. `q` does not quit while a confirmation modal is open.

## Public exposure

`public: true` and `pier share` publish a path on the internet. Anyone who can reach that URL can reach that local service. Pier configures the path; you still own the machine that answers.

`public: 2h` publishes a service for two hours from each `pier up`, then makes it private again on its own. Use it for demos and webhook testing so a public link never outlives the reason for it. See [`docs/configuration.md`](docs/configuration.md#public-for-a-while).

## Safety

Pier only removes routes this project recorded as owned. Unrelated routes on the machine stay untouched unless you pass `--force` to take over a conflicting path. Details: [`docs/troubleshooting.md`](docs/troubleshooting.md).

## Remove Pier routes

```bash
pier down
```

Then delete `pier.yaml` and `.pier/` if you no longer want the project. Runtime ownership state lives under the OS user config directory (`<user-config-dir>/pier/projects/<project-id>.json`) and is not committed.

## Verify

CI runs `gofmt`, `go vet ./...`, and `go test ./...`. Live exposure backends are not required in CI.

Local checks:

```bash
gofmt -w .
go vet ./...
go test ./...
./scripts/smoke.sh
```

`./scripts/smoke.sh` starts two loopback HTTP servers, writes a temporary `pier.yaml`, and runs `pier validate` and `pier plan`. It does not change live routes unless you set `PIER_SMOKE_APPLY=1`.

```bash
PIER_SMOKE_APPLY=1 ./scripts/smoke.sh
```

That apply path needs a disposable machine authorized for the exposure paths you exercise. `pier down` runs in a trap and the script checks that unrelated routes are unchanged.

## License

Pier is available under the [MIT License](LICENSE).
