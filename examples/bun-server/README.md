# Bun server demo

Install Pier from this checkout (the local-name daemon needs a stable binary, so prefer this over `go run`):

```bash
go install ../../cmd/pier
```

Start the loopback server:

```bash
bun run start
```

From another terminal in this directory:

```bash
pier validate
pier plan
pier up
```

`pier up` prints a private Tailscale URL and `https://pier-demo.local/`. The first run asks your OS once to trust Pier's local CA. Open the `.local` URL on this machine, or on a phone on the same Wi-Fi after installing the CA from `http://pier-demo.local/.pier/ca.pem`.

The Tailscale URL is tailnet-only by default. To test Funnel, run:

```bash
pier share web
```

Remove only this demo's routes and local name when finished:

```bash
pier down
```
