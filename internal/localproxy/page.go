package localproxy

import (
	"html"
	"net/http"
	"strconv"
)

func escape(s string) string { return html.EscapeString(s) }

// writePage renders a small self-contained error page. body is trusted HTML;
// every dynamic value in it must already be escaped.
func writePage(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Pier", "1")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>` + escape(title) + ` · Pier</title>
<style>
:root{color-scheme:light dark;--bg:#fafaf9;--fg:#1c1917;--muted:#57534e;--line:#e7e5e4}
@media (prefers-color-scheme:dark){:root{--bg:#0c0a09;--fg:#f5f5f4;--muted:#a8a29e;--line:#292524}}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.55 ui-sans-serif,system-ui,sans-serif}
main{max-width:34rem;margin:12vh auto;padding:0 16px}
small{color:var(--muted);letter-spacing:.08em;text-transform:uppercase}
h1{font-size:1.4rem;margin:.3rem 0 1rem}
code{background:var(--line);padding:.1em .35em;border-radius:4px}
a{color:inherit}
</style></head><body><main><small>Pier · ` + strconv.Itoa(status) + `</small>
<h1>` + escape(title) + `</h1><p>` + body + `</p></main></body></html>`))
}
