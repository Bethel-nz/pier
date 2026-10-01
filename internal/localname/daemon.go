package localname

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/Bethel-nz/pier/internal/certs"
	"github.com/Bethel-nz/pier/internal/localproxy"
	"github.com/Bethel-nz/pier/internal/mdns"
	"github.com/Bethel-nz/pier/internal/state"
)

var (
	httpsPorts = []int{443, 8443, 10443}
	httpPorts  = []int{80, 8080}
)

// idleExit is how long the daemon keeps running with no names to serve.
const idleExit = 3 * time.Second

// Hooks are what the daemon asks of the rest of Pier.
type Hooks struct {
	// Expire closes the project at root's public windows that have ended.
	Expire func(ctx context.Context, root string) error
}

// Run serves every saved .local name and tap, and closes public windows,
// until ctx ends, a stop is requested, or no project needs it anymore. Only
// one daemon runs per user.
func Run(ctx context.Context, projects *state.Store, hooks Hooks) error {
	lock, owned, err := lockDaemon()
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	defer lock.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d := &daemon{
		ctx:           ctx,
		projects:      projects,
		proxy:         localproxy.New(),
		certs:         map[string]loadedCert{},
		taps:          map[tapKey]*tapServer{},
		lanPorts:      map[int]*tapServer{},
		tcp:           map[int]*localproxy.TCPForward{},
		tcpTargets:    map[int]string{},
		captures:      map[string]*projectCapture{},
		tunnels:       map[string]*tunnelProcess{},
		expire:        hooks.Expire,
		expiring:      map[string]*expiry{},
		responderDone: closedChan(),
		beat:          Heartbeat{PID: os.Getpid(), Build: buildID(), StartedAt: time.Now()},
	}
	if port, err := d.serveAPI(); err == nil {
		d.beat.APIPort = port
	} else {
		d.startup = append(d.startup, "Pier could not start its dashboard API: "+err.Error())
	}
	defer removeHeartbeat(d.beat.PID)
	defer d.closeServers()
	defer d.closeLANPorts()
	defer d.closeTunnels()
	defer d.closeTaps() // first: captured requests are written before the heartbeat goes

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
			<-d.responderDone // goodbyes are sent before the heartbeat disappears
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
	ctx      context.Context
	projects *state.Store
	lan      bool // the .local ports are open
	expire   func(ctx context.Context, root string) error
	expiring map[string]*expiry // by project ID
	// responderDone closes once the name publisher has said goodbye.
	responderDone chan struct{}
	proxy         *localproxy.Proxy
	responder     publisher
	servers       []*http.Server
	listeners     []*localproxy.Listeners
	certs         map[string]loadedCert
	taps          map[tapKey]*tapServer
	lanPorts      map[int]*tapServer // plain-HTTP LAN fallback, by port
	tcp           map[int]*localproxy.TCPForward
	tcpTargets    map[int]string
	captures      map[string]*projectCapture // by project ID
	tunnels       map[string]*tunnelProcess  // cloudflared, by project ID
	caPEM         []byte
	startup       []string // warnings found once at startup, such as a port fallback
	beat          Heartbeat

	// published is the last written heartbeat, read by the API goroutines.
	mu        sync.RWMutex
	published Heartbeat
}

// publish writes the heartbeat file and shares a copy with the API.
func (d *daemon) publish() {
	_ = writeHeartbeat(d.beat)
	d.mu.Lock()
	d.published = d.beat
	d.mu.Unlock()
}

func (d *daemon) heartbeat() Heartbeat {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.published
}

// listen binds HTTPS (443, else 8443, else 10443) and HTTP (80, else 8080)
// for redirects and the CA download. A port another program holds on one
// address (Tailscale Serve and Funnel) is shared by binding this machine's
// own addresses instead.
func (d *daemon) listen() error {
	secure := &http.Server{
		Handler:           d.proxy.HTTPS(),
		TLSConfig:         d.proxy.TLSConfig(),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          quietLog(),
	}
	httpsListeners, err := localproxy.Bind(httpsPorts, func(l net.Listener) {
		go func() { _ = secure.ServeTLS(l, "", "") }()
	})
	if err != nil {
		return fmt.Errorf("Pier could not open an HTTPS port for .local names: %w", err)
	}
	if httpsListeners.Port != 443 {
		d.startup = append(d.startup, portHint(httpsListeners.Port, httpsListeners.Skipped))
	}
	d.listeners = append(d.listeners, httpsListeners)
	d.beat.HTTPSPort = httpsListeners.Port
	d.proxy.SetHTTPSPort(httpsListeners.Port)
	d.servers = append(d.servers, secure)

	plain := &http.Server{Handler: d.proxy.HTTP(), ReadHeaderTimeout: 10 * time.Second, ErrorLog: quietLog()}
	if httpListeners, err := localproxy.Bind(httpPorts, func(l net.Listener) {
		go func() { _ = plain.Serve(l) }()
	}); err == nil {
		d.listeners = append(d.listeners, httpListeners)
		d.beat.HTTPPort = httpListeners.Port
		d.servers = append(d.servers, plain)
	}
	return nil
}

// openLAN binds the .local ports and starts publishing names, the first time
// some project has a name. Until then the daemon listens on loopback only.
func (d *daemon) openLAN() error {
	if err := d.listen(); err != nil {
		return err
	}
	d.lan = true
	names, kind, err := newPublisher(d.beat.HTTPSPort)
	if err != nil {
		d.beat.MDNSError = err.Error()
		return nil
	}
	d.responder = names
	d.beat.MDNS = kind
	d.responderDone = make(chan struct{})
	go func() { _ = names.Serve(d.ctx); close(d.responderDone) }()
	return nil
}

func (d *daemon) closeServers() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, server := range d.servers {
		_ = server.Shutdown(ctx)
	}
}

// reconcile matches the proxy, certificates, mDNS names, taps, and public
// windows to saved state, then writes the heartbeat. It reports whether the
// daemon has any work left.
func (d *daemon) reconcile(now time.Time) bool {
	for _, listeners := range d.listeners {
		listeners.Refresh() // follow Wi-Fi changes when a port is shared per address
	}
	warnings := append([]string(nil), d.startup...)
	saved, err := d.projects.List()
	if err != nil {
		d.beat.Error = "Pier could not read project state: " + err.Error()
		d.beat.UpdatedAt = now
		d.publish()
		return true
	}
	d.beat.Error = ""
	routes, conflicts := Routes(saved)
	if len(routes) > 0 && !d.lan {
		if err := d.openLAN(); err != nil {
			// pier up reads the reason from the heartbeat; the next tick tries again.
			d.beat.Error = err.Error()
			d.beat.UpdatedAt = now
			d.publish()
			return true
		}
	}
	d.refreshCA()
	windows, windowWarnings := d.closeWindows(saved, now)
	warnings = append(warnings, windowWarnings...)
	tapped, taps, tapWarnings := d.syncTaps(saved, now)
	warnings = append(warnings, tapWarnings...)
	for _, tap := range taps {
		if tap.Error != "" {
			warnings = append(warnings, tap.Service+": "+tap.Error)
		}
	}

	proxied := make([]localproxy.Route, 0, len(routes))
	served := map[string]*tls.Certificate{}
	statuses := make([]NameStatus, 0, len(routes)+len(conflicts))
	names := make([]string, 0, len(routes))
	lan := map[int]localproxy.Route{}
	tcp := map[int]tcpRoute{}
	for _, route := range routes {
		status := NameStatus{Name: route.Name, Service: route.Service, Project: route.ProjectID, Target: route.Target, LANPort: route.LANPort, TCP: route.TCP}
		if route.TCP {
			// Raw TCP: the name resolves, the port relays; no HTTPS or certificate.
			if route.LANPort != 0 {
				tcp[route.LANPort] = tcpRoute{name: route.Name, target: route.Target}
			}
			names = append(names, route.Name)
			statuses = append(statuses, status)
			continue
		}
		// A tapped service's .local name is throttled and captured like its tap.
		tap := tapped[tapKey{route.ProjectID, route.Service}]
		proxy := localproxy.Route{
			Host: route.Name, Target: route.Target, Service: route.Service, ThisMachineOnly: route.ThisMachineOnly,
			Shaping: tap.Shaping, Capture: tap.Capture,
		}
		if route.LANPort != 0 {
			lan[route.LANPort] = proxy // plain HTTP needs no certificate
		}
		cert, certErr := d.certificate(route.CertFile, route.KeyFile)
		if certErr != nil {
			status.State = StateNoCertificate
			status.Detail = certErr.Error()
			statuses = append(statuses, status)
			continue
		}
		served[route.Name] = cert
		proxied = append(proxied, proxy)
		names = append(names, route.Name)
		statuses = append(statuses, status)
	}
	if err := d.proxy.SetRoutes(proxied); err != nil {
		warnings = append(warnings, err.Error())
	}
	d.proxy.SetCertificates(served)
	failedLAN := d.syncLANPorts(lan)
	for name, detail := range d.syncTCP(tcp) {
		failedLAN[name] = detail
	}
	for i := range statuses {
		if detail, failed := failedLAN[statuses[i].Name]; failed {
			statuses[i].LANPort = 0
			warnings = append(warnings, statuses[i].Name+": "+detail)
		}
	}
	d.beat.LANAddress = ""
	if ip := lanAddress(); ip != nil {
		d.beat.LANAddress = ip.String()
	}

	mdnsState := map[string]mdns.Status{}
	if d.responder != nil {
		d.responder.SetNames(names)
		for _, status := range d.responder.Statuses() {
			mdnsState[status.Name] = status
		}
		if err := d.responder.SendError(); err != nil {
			warnings = append(warnings, d.sendHint(err))
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

	tunnels := d.syncTunnels(saved, now)

	d.beat.Names = statuses
	d.beat.Taps = taps
	d.beat.Tunnels = tunnels
	d.beat.Warnings = warnings
	d.beat.UpdatedAt = now
	d.publish()
	return len(routes) > 0 || len(taps) > 0 || len(tunnels) > 0 || windows
}

// certificate loads a project's leaf, reloading it when the file changes.
func (d *daemon) certificate(certFile, keyFile string) (*tls.Certificate, error) {
	info, err := os.Stat(certFile)
	if err != nil {
		return nil, errors.New("no certificate yet; run pier up in the project")
	}
	modTime := info.ModTime()
	if cached, ok := d.certs[certFile]; ok && cached.modTime.Equal(modTime) {
		return cached.cert, cached.err
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	loaded := loadedCert{modTime: modTime, err: err}
	if err == nil {
		loaded.cert = &pair
	}
	d.certs[certFile] = loaded
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

// sendHint explains a publishing failure: other devices cannot resolve names.
func (d *daemon) sendHint(err error) string {
	if d.beat.MDNS != "pier" {
		return "other devices cannot resolve .local names: " + d.beat.MDNS + " rejected a name (" + err.Error() + ")"
	}
	return blockedHint(err)
}

// blockedHint explains an mDNS send failure: other devices cannot resolve names.
func blockedHint(err error) string {
	hint := "other devices cannot resolve .local names: Pier's mDNS answers fail to send (" + err.Error() + ")"
	if runtime.GOOS == "darwin" {
		hint += ". macOS is likely blocking Pier from the local network: in System Settings → Privacy & Security → Local Network, turn on the terminal app you ran pier up from, then run pier down and pier up from that terminal"
	}
	return hint
}

// portHint explains why .local URLs carry a port, with the fix for each cause.
func portHint(got int, skipped []error) string {
	hint := fmt.Sprintf("port 443 was unavailable, so .local URLs use port %d", got)
	for _, err := range skipped {
		switch {
		case errors.Is(err, syscall.EACCES):
			if exe, exeErr := os.Executable(); exeErr == nil && runtime.GOOS == "linux" {
				return hint + fmt.Sprintf(". To use 443, run: sudo setcap cap_net_bind_service=+ep %s", exe)
			}
		case errors.Is(err, syscall.EADDRINUSE):
			return hint + ". Another program holds 443 on every address; Tailscale Funnel does. Run pier down in the project that shares it, or turn Funnel off, then pier up"
		}
	}
	return hint
}

// quietLog drops TLS handshake noise from browsers that do not trust the CA yet.
func quietLog() *log.Logger { return log.New(io.Discard, "", 0) }
