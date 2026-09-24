package localproxy

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/Bethel-nz/pier/internal/certs"
)

const (
	// InstallPath is the page that walks a device through trusting the Pier CA.
	InstallPath = "/.pier/"
	// CAPath serves the Pier CA as PEM, for tools and scripts.
	CAPath      = "/.pier/ca.pem"
	crtPath     = "/.pier/pier-local-ca.crt"
	profilePath = "/.pier/pier-local-ca.mobileconfig"
)

// caFiles is the Pier CA in each format a device's installer accepts.
type caFiles struct {
	pem         []byte
	der         []byte
	profile     []byte
	fingerprint string
}

func newCAFiles(contents []byte) *caFiles {
	block, _ := pem.Decode(contents)
	if block == nil {
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	return &caFiles{
		pem:         append([]byte(nil), contents...),
		der:         cert.Raw,
		profile:     mobileconfig(cert),
		fingerprint: certs.DisplayFingerprint(cert),
	}
}

// serveInstall answers the install page and CA downloads. It reports whether
// the request was one of them.
func (p *Proxy) serveInstall(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path != "/.pier" && path != InstallPath && path != CAPath && path != crtPath && path != profilePath {
		return false
	}
	p.mu.RLock()
	ca := p.ca
	host := normalizeHost(r.Host)
	_, known := p.routes[host]
	port := p.httpsPort
	p.mu.RUnlock()
	if ca == nil {
		writePage(w, http.StatusNotFound, "No certificate yet", "Run <code>pier up</code> on the machine running Pier first.")
		return true
	}
	switch path {
	case CAPath:
		download(w, "application/x-pem-file", "pier-local-ca.pem", ca.pem)
	case crtPath:
		download(w, "application/x-x509-ca-cert", "pier-local-ca.crt", ca.der)
	case profilePath:
		// Safari hands this type straight to the profile installer.
		w.Header().Set("Content-Type", "application/x-apple-aspen-config")
		_, _ = w.Write(ca.profile)
	default:
		continueURL := ""
		if known {
			authority := host
			if port != 443 {
				authority = net.JoinHostPort(host, strconv.Itoa(port))
			}
			continueURL = "https://" + authority + "/"
		}
		writeHTML(w, http.StatusOK, "trust", "Trust this Pier", installPage(platformOf(r.UserAgent()), ca.fingerprint, continueURL))
	}
	return true
}

func download(w http.ResponseWriter, contentType, name string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(body)
}

// platformOf guesses the visitor's system from its User-Agent. iPads that
// request desktop sites say Macintosh, so the macOS steps point them onward.
func platformOf(userAgent string) string {
	switch {
	case strings.Contains(userAgent, "iPhone"), strings.Contains(userAgent, "iPad"):
		return "ios"
	case strings.Contains(userAgent, "Android"):
		return "android"
	case strings.Contains(userAgent, "Windows"):
		return "windows"
	case strings.Contains(userAgent, "Macintosh"):
		return "macos"
	case strings.Contains(userAgent, "Linux"), strings.Contains(userAgent, "CrOS"):
		return "linux"
	}
	return ""
}

type guide struct {
	id, name, steps string
}

var guides = []guide{
	{"ios", "iPhone and iPad", `<a class="button" href="` + profilePath + `">Download profile</a>
<ol><li>Open this page in Safari, tap <b>Download profile</b>, then <b>Allow</b>.</li>
<li>Settings → General → VPN &amp; Device Management → <b>Pier Local CA</b> → Install.</li>
<li>Settings → General → About → Certificate Trust Settings → turn on <b>Pier Local CA</b>.</li></ol>`},
	{"android", "Android", `<a class="button" href="` + crtPath + `">Download certificate</a>
<ol><li>Tap <b>Download certificate</b>.</li>
<li>Open Settings and search for <b>CA certificate</b> (usually Security → Encryption &amp; credentials → Install a certificate → CA certificate).</li>
<li>Choose <code>pier-local-ca.crt</code> from Downloads and confirm.</li></ol>
<p>Chrome trusts it right away. Some apps only trust built-in certificates.</p>`},
	{"windows", "Windows", `<a class="button" href="` + crtPath + `">Download certificate</a>
<ol><li>Open <code>pier-local-ca.crt</code> and choose <b>Install Certificate</b>.</li>
<li>Pick <b>Current User</b>, then <b>Place all certificates in the following store</b> → <b>Trusted Root Certification Authorities</b>.</li>
<li>Finish, confirm with <b>Yes</b>, and restart the browser.</li></ol>
<p>Or in PowerShell, from Downloads: <code>certutil -user -addstore Root pier-local-ca.crt</code>. Firefox keeps its own list: Settings → Certificates → View Certificates → Authorities → Import.</p>`},
	{"macos", "Mac", `<a class="button" href="` + crtPath + `">Download certificate</a>
<ol><li>If Pier is installed on this Mac, run <code>pier trust</code> instead and skip the rest.</li>
<li>Open <code>pier-local-ca.crt</code> to add it to the login keychain.</li>
<li>In Keychain Access, open <b>Pier Local CA</b> → Trust → <b>Always Trust</b>.</li></ol>
<p>On an iPad, use the iPhone and iPad steps.</p>`},
	{"linux", "Linux", `<a class="button" href="` + crtPath + `">Download certificate</a>
<ol><li>If Pier is installed here, run <code>pier trust</code> instead.</li>
<li>Debian and Ubuntu: <code>sudo cp pier-local-ca.crt /usr/local/share/ca-certificates/ &amp;&amp; sudo update-ca-certificates</code></li>
<li>Chrome and Firefox: <code>certutil -d sql:$HOME/.pki/nssdb -A -t C,, -n "Pier Local CA" -i pier-local-ca.crt</code></li></ol>`},
}

// installPage lists the visitor's platform first and open, the rest folded.
func installPage(platform, fingerprint, continueURL string) string {
	var b strings.Builder
	b.WriteString(`<p>Install the Pier Local CA once on this device. After that, every <code>.local</code> name Pier serves opens without a warning. It can only vouch for <code>.local</code> names.</p>`)
	ordered := make([]guide, 0, len(guides))
	for _, g := range guides {
		if g.id == platform {
			ordered = append([]guide{g}, ordered...)
		} else {
			ordered = append(ordered, g)
		}
	}
	for _, g := range ordered {
		open := ""
		if g.id == platform {
			open = " open"
		}
		fmt.Fprintf(&b, "<details%s><summary>%s</summary>%s</details>", open, g.name, g.steps)
	}
	b.WriteString(`<p>Check that the fingerprint your device shows matches <code>pier doctor</code> on the machine running Pier:</p><p class="fp">SHA-256 ` + escape(fingerprint) + `</p>`)
	if continueURL != "" {
		b.WriteString(`<p>Done? <a href="` + escape(continueURL) + `">Continue to ` + escape(continueURL) + `</a></p>`)
	}
	return b.String()
}

// mobileconfig wraps the CA in an iOS configuration profile. Its identifiers
// come from the fingerprint, so installing it again replaces the old profile.
func mobileconfig(cert *x509.Certificate) []byte {
	sum := sha256.Sum256(cert.Raw)
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>PayloadContent</key><array><dict>
<key>PayloadCertificateFileName</key><string>pier-local-ca.crt</string>
<key>PayloadContent</key><data>` + base64.StdEncoding.EncodeToString(cert.Raw) + `</data>
<key>PayloadDescription</key><string>Trusts HTTPS for .local names served by Pier.</string>
<key>PayloadDisplayName</key><string>Pier Local CA</string>
<key>PayloadIdentifier</key><string>dev.pier.local-ca.certificate</string>
<key>PayloadType</key><string>com.apple.security.root</string>
<key>PayloadUUID</key><string>` + uuidOf(sum[:16]) + `</string>
<key>PayloadVersion</key><integer>1</integer>
</dict></array>
<key>PayloadDescription</key><string>Trusts HTTPS for .local names served by Pier.</string>
<key>PayloadDisplayName</key><string>Pier Local CA</string>
<key>PayloadIdentifier</key><string>dev.pier.local-ca</string>
<key>PayloadRemovalDisallowed</key><false/>
<key>PayloadType</key><string>Configuration</string>
<key>PayloadUUID</key><string>` + uuidOf(sum[16:]) + `</string>
<key>PayloadVersion</key><integer>1</integer>
</dict></plist>
`)
}

func uuidOf(b []byte) string {
	return fmt.Sprintf("%X-%X-%X-%X-%X", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
