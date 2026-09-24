package localproxy

import (
	"html"
	"net/http"
	"strconv"
)

func escape(s string) string { return html.EscapeString(s) }

// logoPath is the Pier mark (docs/pier.svg), drawn in the page's text color.
const logoPath = `M40 0h246a4 4 0 0 1 4 4v23a4 4 0 0 1-4 4H40a4 4 0 0 1-4-4V4a4 4 0 0 1 4-4ZM4 49h318a4 4 0 0 1 4 4v29a4 4 0 0 1-4 4h-50v77a4 4 0 0 1-4 4h-46a4 4 0 0 1-4-4V86H107v77a4 4 0 0 1-4 4H58a4 4 0 0 1-4-4V86H4a4 4 0 0 1-4-4V53a4 4 0 0 1 4-4Z`

// favicon is the mark as a data URL, dark on light tabs and light on dark ones.
const favicon = `data:image/svg+xml,` +
	`%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 -80 326 326'%3E` +
	`%3Cstyle%3Epath%7Bfill:%231c1917%7D@media (prefers-color-scheme:dark)%7Bpath%7Bfill:%23f5f5f4%7D%7D%3C/style%3E` +
	`%3Cpath d='` + logoPath + `'/%3E%3C/svg%3E`

// writePage renders a small self-contained error page. body is trusted HTML;
// every dynamic value in it must already be escaped.
func writePage(w http.ResponseWriter, status int, title, body string) {
	writeHTML(w, status, strconv.Itoa(status), title, "<p>"+body+"</p>")
}

// writeHTML renders a Pier page. content is trusted HTML placed after the title.
func writeHTML(w http.ResponseWriter, status int, label, title, content string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Pier", "1")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>` + escape(title) + ` · Pier</title>
<link rel="icon" href="` + favicon + `">
<style>
:root{color-scheme:light dark;--bg:#fafaf9;--fg:#1c1917;--muted:#57534e;--line:#e7e5e4}
@media (prefers-color-scheme:dark){:root{--bg:#0c0a09;--fg:#f5f5f4;--muted:#a8a29e;--line:#292524}}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.55 ui-sans-serif,system-ui,sans-serif}
main{max-width:34rem;margin:12vh auto;padding:0 16px}
small{color:var(--muted);letter-spacing:.08em;text-transform:uppercase}
h1{font-size:1.4rem;margin:.3rem 0 1rem}
code{background:var(--line);padding:.1em .35em;border-radius:4px}
a{color:inherit}
.button{display:inline-block;margin:.25rem 0 1rem;padding:.6rem 1rem;border-radius:8px;background:var(--fg);color:var(--bg);text-decoration:none;font-weight:600}
ol{padding-left:1.3rem}
li{margin:.3rem 0}
details{border-top:1px solid var(--line);padding:.6rem 0}
summary{cursor:pointer;font-weight:600}
.fp{font:12px/1.5 ui-monospace,monospace;word-break:break-all;color:var(--muted)}
.logo{display:block;width:40px;height:21px;margin-bottom:1rem;fill:var(--fg)}
</style></head><body><main><svg class="logo" viewBox="0 0 326 167" aria-hidden="true"><path d="` + logoPath + `"/></svg>
<small>Pier · ` + escape(label) + `</small>
<h1>` + escape(title) + `</h1>` + content + `</main></body></html>`))
}
