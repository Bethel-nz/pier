// Package localproxy serves Pier's .local names: HTTPS on the LAN, routed by
// Host to services that stay bound to loopback.
package localproxy

import (
	"crypto/tls"
	"errors"
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
	// CAPath serves the Pier CA over plain HTTP so phones can install it.
	CAPath = "/.pier/ca.pem"
)

// Route sends one .local host to one loopback target.
type Route struct {
	Host    string
	Target  string
	Service string
}

type route struct {
	Route
	target *url.URL
	proxy  *httputil.ReverseProxy
}

// Proxy holds the live route table, certificates, and CA.
type Proxy struct {
	mu        sync.RWMutex
	routes    map[string]*route
	certs     map[string]*tls.Certificate
	caPEM     []byte
	httpsPort int
}

// New returns an empty proxy. Routes and certificates are set by the daemon.
func New() *Proxy {
	return &Proxy{routes: map[string]*route{}, certs: map[string]*tls.Certificate{}, httpsPort: 443}
}

// SetRoutes replaces the route table. An invalid target rejects the whole set.
func (p *Proxy) SetRoutes(routes []Route) error {
	next := make(map[string]*route, len(routes))
	for _, r := range routes {
		target, err := url.Parse(r.Target)
		if err != nil || target.Host == "" {
			return fmt.Errorf("invalid target %q for %s", r.Target, r.Host)
		}
		built := &route{Route: r, target: target}
		built.proxy = p.reverseProxy(built)
		next[normalizeHost(r.Host)] = built
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

// SetCA sets the CA certificate offered at CAPath.
func (p *Proxy) SetCA(pem []byte) {
	p.mu.Lock()
	p.caPEM = append([]byte(nil), pem...)
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops, _ := strconv.Atoi(r.Header.Get(hopHeader))
		if hops >= maxHops {
			writePage(w, http.StatusLoopDetected, "This request is going in circles",
				"It has passed through Pier "+strconv.Itoa(hops)+" times. A dev server is probably proxying back to its own .local name without rewriting the Host header. Set <code>changeOrigin: true</code> in your Vite or webpack proxy config.")
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
		found.proxy.ServeHTTP(w, r)
	})
}

// HTTP redirects known hosts to HTTPS and serves the CA for phones.
func (p *Proxy) HTTP() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == CAPath {
			p.mu.RLock()
			ca := p.caPEM
			p.mu.RUnlock()
			if len(ca) == 0 {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/x-x509-ca-cert")
			w.Header().Set("Content-Disposition", `attachment; filename="pier-local-ca.pem"`)
			_, _ = w.Write(ca)
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
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			hops, _ := strconv.Atoi(pr.In.Header.Get(hopHeader))
			pr.SetURL(rt.target)
			pr.SetXForwarded()
			if port := portOf(pr.In); port != "" {
				pr.Out.Header.Set("X-Forwarded-Port", port)
			}
			pr.Out.Header.Set(hopHeader, strconv.Itoa(hops+1))
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writePage(w, http.StatusBadGateway, rt.Service+" is not responding",
				"Pier routes <b>"+escape(rt.Host)+"</b> to <code>"+escape(rt.target.Host)+"</code>, but nothing answered there. Start the service, then reload.")
		},
	}
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

func isLoopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Listen binds the first free port in ports on every interface.
// It returns the port it got so URLs can include it when it is not the default.
func Listen(ports []int) (net.Listener, int, error) {
	var errs []error
	for _, port := range ports {
		listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
		if err == nil {
			return listener, port, nil
		}
		errs = append(errs, fmt.Errorf("port %d: %w", port, err))
	}
	return nil, 0, errors.Join(errs...)
}
