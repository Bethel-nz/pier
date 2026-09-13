#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
pier=(go run "$root/cmd/pier")

tmp="$(mktemp -d "${TMPDIR:-/tmp}/pier-smoke.XXXXXX")"
cleanup() {
  if [[ -n "${up_applied:-}" ]]; then
    (cd "$tmp" && "${pier[@]}" down) || true
  fi
  if [[ -n "${server_pids:-}" ]]; then
    # shellcheck disable=SC2086
    kill $server_pids 2>/dev/null || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

pick_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

port_web="$(pick_port)"
port_api="$(pick_port)"

python3 - <<PY &
from http.server import BaseHTTPRequestHandler, HTTPServer

class H(BaseHTTPRequestHandler):
    def do_GET(self):
        body = b"web"
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, format, *args):
        return

HTTPServer(("127.0.0.1", int("$port_web")), H).serve_forever()
PY
pid_web=$!

python3 - <<PY &
from http.server import BaseHTTPRequestHandler, HTTPServer

class H(BaseHTTPRequestHandler):
    def do_GET(self):
        body = b"api"
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, format, *args):
        return

HTTPServer(("127.0.0.1", int("$port_api")), H).serve_forever()
PY
pid_api=$!
server_pids="$pid_web $pid_api"

cat > "$tmp/pier.yaml" <<EOF
version: 1
name: pier-smoke

services:
  web:
    target: 127.0.0.1:${port_web}
    path: /

  api:
    target: 127.0.0.1:${port_api}
    path: /api
EOF

cd "$tmp"
"${pier[@]}" validate
"${pier[@]}" plan

before=""
if command -v tailscale >/dev/null 2>&1; then
  before="$(tailscale serve status --json 2>/dev/null || true)"
fi

if [[ "${PIER_SMOKE_APPLY:-}" != "1" ]]; then
  echo "skipping pier up (set PIER_SMOKE_APPLY=1 to apply on a disposable Tailscale node)"
  exit 0
fi

"${pier[@]}" up
up_applied=1

web_url="$("${pier[@]}" --json status | python3 -c 'import json,sys; d=json.load(sys.stdin); print([s["url"] for s in d["data"]["services"] if s["name"]=="web"][0])')"
api_url="$("${pier[@]}" --json status | python3 -c 'import json,sys; d=json.load(sys.stdin); print([s["url"] for s in d["data"]["services"] if s["name"]=="api"][0])')"

curl -fsS "$web_url" | grep -qx web
curl -fsS "$api_url" | grep -qx api

"${pier[@]}" down
up_applied=

after="$(tailscale serve status --json 2>/dev/null || true)"
if [[ "$before" != "$after" ]]; then
  echo "unrelated Tailscale routes changed" >&2
  exit 1
fi
