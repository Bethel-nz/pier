# Mobile apps with local names

Point a phone, simulator, or emulator at services on your machine through `.local` names instead of IP addresses. The names keep working when you switch networks, for example from home Wi-Fi to a phone hotspot, because Pier re-registers them with your machine's new address within a couple of seconds.

This walkthrough uses a React Native / Expo app with an HTTP API on port 4000 and a WebSocket server on port 3001. Adjust the names and ports to yours.

## 1. Install and initialise

```bash
go install github.com/Bethel-nz/pier/cmd/pier@latest   # or download a release binary
cd your-project
pier init
```

## 2. Describe the services

Give each server its own `local` name. `protocol: http` means your servers speak plain HTTP on those ports; Pier adds TLS in front, so clients still use `https://` and `wss://`.

```yaml
version: 1
name: your-app

defaults:
  public: false
  protocol: http

services:
  api:
    target: localhost:4000
    path: /
    local: myapp-api.local
  ws:
    target: localhost:3001
    path: /ws
    local: myapp-ws.local
```

`path` applies to the HTTPS exposure path (see [`configuration.md`](configuration.md)). Local names always map `/` to the target.

## 3. Bring them up

Start your servers, then:

```bash
pier up
```

```
local  api  https://myapp-api.local/
lan    api  http://192.168.1.162:4100/  (any device on this network, no setup)
local  ws   https://myapp-ws.local/
lan    ws   http://192.168.1.162:4101/  (any device on this network, no setup)
```

The first `pier up` asks macOS once for your password to trust Pier's CA. Run it from your own terminal so the dialog can appear; if it was skipped, run `pier trust`.

Check both from the machine:

```bash
curl https://myapp-api.local/
curl --http1.1 -o /dev/null -w '%{http_code}\n' \
  -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
  -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
  https://myapp-ws.local/        # 101 means the WebSocket upgrade works
```

## 4. Point the app at the names

```bash
# apps/native/.env
EXPO_PUBLIC_SERVER_URL=https://myapp-api.local
EXPO_PUBLIC_WS_URL=wss://myapp-ws.local
```

`EXPO_PUBLIC_*` values are baked into the JavaScript bundle, so restart Metro with a clear cache. No native rebuild is needed:

```bash
npx expo start -c
```

## 5. Trust the CA on each device, once

Each device and simulator has its own trust store. Do this once per device; it covers every `.local` name Pier serves.

| Where the app runs | What to do |
|---|---|
| iPhone / iPad | In Safari open `http://myapp-api.local/.pier/` (or AirDrop `~/Library/Application Support/pier/ca/ca.pem`). Install it in Settings → General → VPN & Device Management, then turn on **Pier Local CA** in Settings → General → About → Certificate Trust Settings. |
| iOS Simulator | `xcrun simctl keychain booted add-root-cert "$HOME/Library/Application Support/pier/ca/ca.pem"`. Repeat after erasing the simulator. |
| Android device or emulator | Install `ca.pem` as a CA certificate. Apps ignore user CAs by default, so see below. |

Then force-quit the app and reopen it. iOS apps keep the trust they started with.

## Android apps

Android apps ignore user-installed CAs unless the app opts in. For debug builds, add a network security config that trusts user certificates, or point the app at the `lan` address instead (plain HTTP, so allow cleartext for that host in debug).

## When something fails

| Symptom | Cause | Fix |
|---|---|---|
| `The certificate for this server is invalid` (NSURLErrorDomain -1202) | The device or simulator doesn't trust Pier's CA | Step 5. On iPhone, check Certificate Trust Settings. On the Simulator, run the `simctl` command. |
| The name doesn't resolve on the device, but the `lan` address works | Name lookup, usually IPv6 off on one side or a router that drops multicast | See [Gotchas](../README.md#gotchas) |
| Works on the Mac, fails on the device after switching networks | The device and the Mac are on different networks | Put both on the same network; Pier follows the Mac's new address by itself |
| WebSocket connects locally but not through the name | Wrong scheme | Use `wss://`, not `ws://`, with a local name |

The `lan` addresses need no certificate or name lookup, which makes them a good quick check. They contain the IP, though, so they change when you switch networks. Use the `.local` names in your app's config.
