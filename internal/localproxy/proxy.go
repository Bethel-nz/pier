// Package localproxy serves Pier's .local names: HTTPS on the LAN, routed by
// Host to services that stay bound to loopback.
package localproxy

import (
	"github.com/Bethel-nz/pier/internal/capture"

	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	hopHeader = "X-Pier-Hops"
	maxHops   = 5
)

// Route sends one .local host, or one tap, to one loopback target.
type Route struct {
	Host    string
	Target  string
	Service string
	// ThisMachineOnly refuses clients other than the machine running Pier.
	ThisMachineOnly bool
	// Shaping slows the service down; the zero value is full speed.
	Shaping Shaping
	// Capture receives every finished request when set.
	Capture Recorder
}

// Recorder keeps requests for replay.
type Recorder interface {
	Record(capture.Exchange)
}

type route struct {
	Route
	target  *url.URL
	handler http.Handler // shaping, capture, then the reverse proxy
}

// build parses r's target and assembles its handler chain.
func (p *Proxy) build(r Route) (*route, error) {
	target, err := url.Parse(r.Target)
	if err != nil || target.Host == "" {
		return nil, fmt.Errorf("invalid target %q for %s", r.Target, r.Service)
	}
	built := &route{Route: r, target: target}
	built.handler = shape(r.Shaping, captureTo(r.Capture, r.Service, r.Host == "", p.reverseProxy(built)))
	return built, nil
}

// Direct serves one .local route on a port of its own, whatever Host the
// client sent: the plain-HTTP LAN fallback for devices that cannot resolve
// .local. r.Host still names the route in error pages and captures.
func (p *Proxy) Direct(r Route) (http.Handler, error) {
	built, err := p.build(r)
	if err != nil {
		return nil, err
	}
	return p.record(built.handler), nil
}

// Tap serves one service on its own loopback port, for traffic that reaches
// it without a .local name: Tailscale Serve and Funnel point at the tap.
func (p *Proxy) Tap(r Route) (http.Handler, error) {
	built, err := p.build(r)
	if err != nil {
		return nil, err
	}
	return p.record(built.handler), nil
}

// Proxy holds the live route table, certificates, and CA.
type Proxy struct {
	mu        sync.RWMutex
	routes    map[string]*route
	certs     map[string]*tls.Certificate
	ca        *caFiles
	httpsPort int
	traffic   *traffic
}

// New returns an empty proxy. Routes and certificates are set by the daemon.
func New() *Proxy {
	return &Proxy{routes: map[string]*route{}, certs: map[string]*tls.Certificate{}, httpsPort: 443, traffic: newTraffic()}
}

// SetRoutes replaces the route table. An invalid target rejects the whole set.
// Unchanged routes keep their proxy, and with it their idle connections.
func (p *Proxy) SetRoutes(routes []Route) error {
	p.mu.RLock()
	current := p.routes
	p.mu.RUnlock()
	next := make(map[string]*route, len(routes))
	for _, r := range routes {
		host := normalizeHost(r.Host)
		if existing, ok := current[host]; ok && existing.Route == r {
			next[host] = existing
			continue
		}
		built, err := p.build(r)
		if err != nil {
			return err
		}
		next[host] = built
	}
	p.mu.Lock()
	p.routes = next
	p.mu.Unlock()
	return nil
}

// SetCertificates maps each host to the certificate served for it.
func (p *Proxy) SetCertificates(certs map[string]*tls.Certificate) {
	next := make(map[string]*tls.Certificate, len(certs))
	for host, cert := range certs {
		next[normalizeHost(host)] = cert
	}
	p.mu.Lock()
	p.certs = next
	p.mu.Unlock()
}

// SetCA sets the CA certificate the install page offers.
func (p *Proxy) SetCA(pem []byte) {
	ca := newCAFiles(pem)
	p.mu.Lock()
	p.ca = ca
	p.mu.Unlock()
}

// SetHTTPSPort sets the port HTTP redirects point at.
func (p *Proxy) SetHTTPSPort(port int) {
	p.mu.Lock()
	p.httpsPort = port
	p.mu.Unlock()
}

// TLSConfig picks the project certificate by SNI and offers HTTP/2.
func (p *Proxy) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"h2", "http/1.1"},
		GetCertificate: p.getCertificate,
	}
}

func (p *Proxy) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if cert, ok := p.certs[normalizeHost(hello.ServerName)]; ok {
		return cert, nil
	}
	return nil, fmt.Errorf("no Pier certificate for %q", hello.ServerName)
}

// HTTPS is the handler behind TLS: route by Host, forward to loopback.
func (p *Proxy) HTTPS() http.Handler {
	return p.record(p.route())
}

func (p *Proxy) route() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops, _ := strconv.Atoi(r.Header.Get(hopHeader))
		if hops >= maxHops {
			writePage(w, http.StatusLoopDetected, "This request is going in circles",
				"It has passed through Pier "+strconv.Itoa(hops)+" times. A dev server is probably proxying back to its own .local name without rewriting the Host header. Set <code>changeOrigin: true</code> in your Vite or webpack proxy config.")
			return
		}
		// Also over HTTPS: browsers that upgrade every link land here, past the warning.
		if p.serveInstall(w, r) {
			return
		}
		host := normalizeHost(r.Host)
		p.mu.RLock()
		found := p.routes[host]
		p.mu.RUnlock()
		if found == nil {
			p.notFound(w, r, host)
			return
		}
		if found.ThisMachineOnly && !fromThisMachine(r) {
			writePage(w, http.StatusForbidden, "Only on the machine running Pier",
				"<b>"+escape(host)+"</b> is set to <code>local.lan: false</code>, so Pier serves it to this computer only.")
			return
		}
		found.handler.ServeHTTP(w, r)
	})
}

// HTTP redirects known hosts to HTTPS and serves the install page for devices
// that do not trust the Pier CA yet.
func (p *Proxy) HTTP() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.serveInstall(w, r) {
			return
		}
		host := normalizeHost(r.Host)
		p.mu.RLock()
		_, known := p.routes[host]
		port := p.httpsPort
		p.mu.RUnlock()
		if !known {
			p.notFound(w, r, host)
			return
		}
		authority := host
		if port != 443 {
			authority = net.JoinHostPort(host, strconv.Itoa(port))
		}
		target := url.URL{Scheme: "https", Host: authority, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
		http.Redirect(w, r, target.String(), http.StatusPermanentRedirect)
	})
}

func (p *Proxy) reverseProxy(rt *route) *httputil.ReverseProxy {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if rt.target.Scheme == "https" {
		// The target is loopback only (validated in pier.yaml), and its own
		// certificate is for localhost, not for the .local name.
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	var roundTripper http.RoundTripper = transport
	if rt.target.Scheme == "http" {
		// gRPC needs HTTP/2 end to end; plain-HTTP gRPC servers speak it unencrypted (h2c).
		grpc := http.DefaultTransport.(*http.Transport).Clone()
		grpc.Protocols = new(http.Protocols)
		grpc.Protocols.SetUnencryptedHTTP2(true)
		roundTripper = splitGRPC{grpc: grpc, other: transport}
	}
	return &httputil.ReverseProxy{
		Transport: roundTripper,
		Rewrite: func(pr *httputil.ProxyRequest) {
			hops, _ := strconv.Atoi(pr.In.Header.Get(hopHeader))
			pr.SetURL(rt.target)
			pr.SetXForwarded()
			if rt.Host == "" {
				keepForwarded(pr) // a tap's client is tailscaled, which already said who asked
			} else if port := portOf(pr.In); port != "" {
				pr.Out.Header.Set("X-Forwarded-Port", port)
			}
			pr.Out.Header.Set(hopHeader, strconv.Itoa(hops+1))
			translateOrigin(pr, rt.target)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writePage(w, http.StatusBadGateway, rt.Service+" is not responding",
				"Pier routes <b>"+escape(normalizeHost(r.Host))+"</b> to <code>"+escape(rt.target.Host)+"</code>, but nothing answered there. Start the service, then reload.")
		},
	}
}

// keepForwarded restores the X-Forwarded headers a trusted local proxy set,
// which SetXForwarded replaced with loopback values.
func keepForwarded(pr *httputil.ProxyRequest) {
	for _, name := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
		if values := pr.In.Header.Values(name); len(values) > 0 {
			pr.Out.Header[name] = values
		}
	}
}

// splitGRPC sends gRPC over h2c and everything else over HTTP/1.1, which is
// all most dev servers speak.
type splitGRPC struct {
	grpc, other http.RoundTripper
}

func (s splitGRPC) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.ProtoMajor == 2 && strings.HasPrefix(req.Header.Get("Content-Type"), "application/grpc") &&
		!strings.HasPrefix(req.Header.Get("Content-Type"), "application/grpc-web") {
		return s.grpc.RoundTrip(req)
	}
	return s.other.RoundTrip(req)
}

// translateOrigin makes a same-origin read look same-origin to the upstream.
//
// Dev servers guard their internals by Origin (Next.js blocks /_next and its
// HMR socket for origins other than localhost), and Pier already rewrites Host
// to the target. When the browser says a GET or HEAD came from this very .local
// page, the Origin is translated the same way. Anything else passes unchanged:
// a foreign origin still gets blocked, and state-changing requests keep the
// public origin so CSRF checks can compare it with X-Forwarded-Host.
func translateOrigin(pr *httputil.ProxyRequest, target *url.URL) {
	if pr.In.Method != http.MethodGet && pr.In.Method != http.MethodHead {
		return
	}
	origin, err := url.Parse(pr.In.Header.Get("Origin"))
	if err != nil || origin.Scheme != "https" || !strings.EqualFold(origin.Host, pr.In.Host) {
		return
	}
	pr.Out.Header.Set("Origin", target.Scheme+"://"+target.Host)
}

func (p *Proxy) notFound(w http.ResponseWriter, r *http.Request, host string) {
	body := "Pier has no service at <b>" + escape(host) + "</b>."
	if isLoopback(r.RemoteAddr) {
		p.mu.RLock()
		hosts := make([]string, 0, len(p.routes))
		for name := range p.routes {
			hosts = append(hosts, name)
		}
		p.mu.RUnlock()
		sort.Strings(hosts)
		if len(hosts) > 0 {
			body += " Names on this machine:<ul>"
			for _, name := range hosts {
				body += `<li><a href="https://` + escape(name) + `/">` + escape(name) + "</a></li>"
			}
			body += "</ul>"
		}
	}
	writePage(w, http.StatusNotFound, "Unknown name", body)
}

func portOf(r *http.Request) string {
	if _, port, err := net.SplitHostPort(r.Host); err == nil {
		return port
	}
	if r.TLS != nil {
		return "443"
	}
	return "80"
}

func normalizeHost(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

// fromThisMachine reports whether the client is this computer: loopback, or a
// connection whose source address is the address it arrived on.
func fromThisMachine(r *http.Request) bool {
	if isLoopback(r.RemoteAddr) {
		return true
	}
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return false
	}
	localHost, _, err := net.SplitHostPort(local.String())
	if err != nil {
		return false
	}
	remoteIP, localIP := net.ParseIP(remote), net.ParseIP(localHost)
	return remoteIP != nil && remoteIP.Equal(localIP)
}

func isLoopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
