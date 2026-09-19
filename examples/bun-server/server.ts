const port = Number(Bun.env.PORT ?? 4000);

if (!Number.isInteger(port) || port < 1 || port > 65535) {
  throw new Error("PORT must be an integer between 1 and 65535");
}

const server = Bun.serve({
  hostname: "127.0.0.1",
  port,
  fetch(request) {
    const url = new URL(request.url);

    if (url.pathname === "/health") {
      return Response.json({ status: "ok" });
    }

    return new Response("Hello from Pier's Bun server!\n", {
      headers: { "content-type": "text/plain; charset=utf-8" },
    });
  },
});

console.log(`Bun server listening on ${server.url}`);
