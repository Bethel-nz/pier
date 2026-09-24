package localname

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Bethel-nz/pier/internal/certs"
	"github.com/Bethel-nz/pier/internal/localproxy"
	"github.com/Bethel-nz/pier/internal/mdns"
	"github.com/Bethel-nz/pier/internal/state"
)

var (
	httpsPorts = []int{443, 8443}
	httpPorts  = []int{80, 8080}
)

// idleExit is how long the daemon keeps running with no names to serve.
const idleExit = 3 * time.Second

// Run serves every saved .local name until ctx ends, a stop is requested, or
// no project declares a name anymore. Only one daemon runs per user.
func Run(ctx context.Context, projects *state.Store) error {
	lock, owned, err := lockDaemon()
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	defer lock.Close()

	d := &daemon{
		projects: projects,
		proxy:    localproxy.New(),
		certs:    map[string]loadedCert{},
		beat:     Heartbeat{PID: os.Getpid(), Build: buildID(), StartedAt: time.Now()},
	}

	if err := d.listen(); err != nil {
		// Leave this heartbeat behind: pier up reads the reason from it.
		d.beat.Error = err.Error()
		d.beat.UpdatedAt = time.Now()
		_ = writeHeartbeat(d.beat)
		return err
	}
	defer removeHeartbeat(d.beat.PID)
	defer d.closeServers()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	responderDone := make(chan struct{})
	if responder, err := mdns.Listen(mdns.Options{}); err != nil {
		d.beat.MDNSError = err.Error()
		close(responderDone)
	} else {
		d.responder = responder
		go func() { _ = responder.Serve(ctx); close(responderDone) }()
	}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	idleSince := time.Time{}
	for {
		active := d.reconcile(time.Now())
		switch {
		case active:
			idleSince = time.Time{}
		case idleSince.IsZero():
			idleSince = time.Now()
		case time.Since(idleSince) > idleExit:
			cancel()
		}
		if stopRequested(d.beat.PID) {
			cancel()
		}
		select {
		case <-ctx.Done():
			<-responderDone // goodbyes are sent before the heartbeat disappears
			return nil
		case <-tick.C:
		}
	}
}

type loadedCert struct {
	modTime time.Time
	cert    *tls.Certificate
	err     error
}

type daemon struct {
	projects  *state.Store
	proxy     *localproxy.Proxy
	responder *mdns.Responder
	servers   []*http.Server
	certs     map[string]loadedCert
	caPEM     []byte
	startup   []string // warnings found once at startup, such as a port fallback
	beat      Heartbeat
}

// listen binds HTTPS (443, else 8443) on every interface, and HTTP (80, else
// 8080) for redirects and the CA download.
func (d *daemon) listen() error {
	httpsListener, httpsPort, err := localproxy.Listen(httpsPorts)
	if err != nil {
		return fmt.Errorf("Pier could not open an HTTPS port for .local names: %w", err)
	}
	if httpsPort != 443 {
		d.startup = append(d.startup, portHint(443, httpsPort))
	}
	d.beat.HTTPSPort = httpsPort
	d.proxy.SetHTTPSPort(httpsPort)
	secure := &http.Server{
		Handler:           d.proxy.HTTPS(),
		TLSConfig:         d.proxy.TLSConfig(),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          quietLog(),
	}
	go func() { _ = secure.ServeTLS(httpsListener, "", "") }()
	d.servers = append(d.servers, secure)

	if httpListener, httpPort, err := localproxy.Listen(httpPorts); err == nil {
		d.beat.HTTPPort = httpPort
		plain := &http.Server{Handler: d.proxy.HTTP(), ReadHeaderTimeout: 10 * time.Second, ErrorLog: quietLog()}
		go func() { _ = plain.Serve(httpListener) }()
		d.servers = append(d.servers, plain)
	}
	return nil
}

func (d *daemon) closeServers() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, server := range d.servers {
		_ = server.Shutdown(ctx)
	}
}

// reconcile matches the proxy, certificates, and mDNS names to saved state,
// then writes the heartbeat. It reports whether any name is being served.
func (d *daemon) reconcile(now time.Time) bool {
	warnings := append([]string(nil), d.startup...)
	saved, err := d.projects.List()
	if err != nil {
		d.beat.Error = "Pier could not read project state: " + err.Error()
		d.beat.UpdatedAt = now
		_ = writeHeartbeat(d.beat)
		return true
	}
	d.beat.Error = ""
	routes, conflicts := Routes(saved)
	d.refreshCA()

	proxied := make([]localproxy.Route, 0, len(routes))
	served := map[string]*tls.Certificate{}
	statuses := make([]NameStatus, 0, len(routes)+len(conflicts))
	names := make([]string, 0, len(routes))
	for _, route := range routes {
		status := NameStatus{Name: route.Name, Service: route.Service, Project: route.ProjectID, Target: route.Target}
		cert, certErr := d.certificate(route.CertDir)
		if certErr != nil {
			status.State = StateNoCertificate
			status.Detail = certErr.Error()
			statuses = append(statuses, status)
			continue
		}
		served[route.Name] = cert
		proxied = append(proxied, localproxy.Route{Host: route.Name, Target: route.Target, Service: route.Service})
		names = append(names, route.Name)
		statuses = append(statuses, status)
	}
	if err := d.proxy.SetRoutes(proxied); err != nil {
		warnings = append(warnings, err.Error())
	}
	d.proxy.SetCertificates(served)

	mdnsState := map[string]mdns.Status{}
	if d.responder != nil {
		d.responder.SetNames(names)
		for _, status := range d.responder.Statuses() {
			mdnsState[status.Name] = status
		}
	}
	for i := range statuses {
		if statuses[i].State != "" {
			continue
		}
		switch {
		case d.responder == nil:
			statuses[i].State = StateNoMDNS
			statuses[i].Detail = d.beat.MDNSError
		case mdnsState[statuses[i].Name].State == mdns.Conflict:
			statuses[i].State = StateConflict
			statuses[i].Detail = "another device at " + mdnsState[statuses[i].Name].Other + " already answers for this name"
		case mdnsState[statuses[i].Name].State == mdns.Live:
			statuses[i].State = StateLive
		default:
			statuses[i].State = StateProbing
		}
	}
	for _, conflict := range conflicts {
		statuses = append(statuses, NameStatus{
			Name: conflict.Name, Project: conflict.ProjectID, State: StateConflict,
			Detail: "another Pier project already serves this name",
		})
	}

	d.beat.Names = statuses
	d.beat.Warnings = warnings
	d.beat.UpdatedAt = now
	_ = writeHeartbeat(d.beat)
	return len(routes) > 0
}

// certificate loads a project's leaf, reloading it when the file changes.
func (d *daemon) certificate(dir string) (*tls.Certificate, error) {
	modTime, err := certs.LeafModTime(dir)
	if err != nil {
		return nil, errors.New("no certificate yet; run pier up in the project")
	}
	if cached, ok := d.certs[dir]; ok && cached.modTime.Equal(modTime) {
		return cached.cert, cached.err
	}
	pair, err := certs.LoadLeaf(dir)
	loaded := loadedCert{modTime: modTime, err: err}
	if err == nil {
		loaded.cert = &pair
	}
	d.certs[dir] = loaded
	return loaded.cert, loaded.err
}

func (d *daemon) refreshCA() {
	dir, err := certs.DefaultCADir()
	if err != nil {
		return
	}
	contents, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil || string(contents) == string(d.caPEM) {
		return
	}
	d.caPEM = contents
	d.proxy.SetCA(contents)
}

func portHint(wanted, got int) string {
	hint := fmt.Sprintf("port %d was unavailable, so .local URLs use port %d", wanted, got)
	if exe, err := os.Executable(); err == nil && runtime.GOOS == "linux" {
		hint += fmt.Sprintf(". To use %d, run: sudo setcap cap_net_bind_service=+ep %s", wanted, exe)
	}
	return hint
}

// quietLog drops TLS handshake noise from browsers that do not trust the CA yet.
func quietLog() *log.Logger { return log.New(io.Discard, "", 0) }
