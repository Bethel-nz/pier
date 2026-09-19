# Bun server demo

Start the loopback server:

```bash
bun run start
```

From another terminal in this directory:

```bash
go run ../../cmd/pier validate
go run ../../cmd/pier plan
go run ../../cmd/pier up
```

The service is tailnet-only by default. To test Funnel, run:

```bash
go run ../../cmd/pier share web
```

Remove only this demo's routes when finished:

```bash
go run ../../cmd/pier down
```
