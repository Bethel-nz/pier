package localname

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Bethel-nz/pier/internal/capture"
	"github.com/Bethel-nz/pier/internal/localproxy"
	"github.com/Bethel-nz/pier/internal/state"
)

// TapStatus is one tap as the heartbeat reports it.
type TapStatus struct {
	Project string `json:"project"`
	Service string `json:"service"`
	Port    int    `json:"port"`
	Error   string `json:"error,omitempty"`
}

type tapKey struct{ project, service string }

// tapServer listens on one tap port. Its handler is swapped in place when the
// throttle or capture changes, so Tailscale never sees the port close.
type tapServer struct {
	port    int
	route   localproxy.Route
	handler atomic.Pointer[http.Handler]
	server  *http.Server
	err     error
}

// projectCapture writes one project's captures to its .pier/capture.db.
type projectCapture struct {
	path     string
	store    *capture.Store
	recorder *capture.Recorder
	stop     context.CancelFunc
	done     chan struct{}
	keep     map[string]time.Duration
	pruned   time.Time
}

// pruneEvery is how often expired captures are deleted.
const pruneEvery = time.Minute

// syncTaps opens, updates, and closes taps and capture files to match saved
// state. It returns each tapped service's route, for its .local name to share.
func (d *daemon) syncTaps(saved []state.ProjectState, now time.Time) (map[tapKey]localproxy.Route, []TapStatus, []string) {
	var warnings []string
	wanted := map[tapKey]state.Tap{}
	keep := map[string]map[string]time.Duration{}
	paths := map[string]string{}
	for _, project := range saved {
		if project.Path == "" {
			continue
		}
		for _, tap := range project.Taps {
			wanted[tapKey{project.ProjectID, tap.Service}] = tap
			if tap.CaptureSeconds > 0 {
				if keep[project.ProjectID] == nil {
					keep[project.ProjectID] = map[string]time.Duration{}
				}
				keep[project.ProjectID][tap.Service] = time.Duration(tap.CaptureSeconds) * time.Second
				paths[project.ProjectID] = project.Path
			}
		}
	}

	for id, pc := range d.captures {
		if keep[id] == nil || pc.path != capture.PathFor(paths[id]) {
			pc.close()
			delete(d.captures, id)
		}
	}
	for id, services := range keep {
		pc, err := d.captureFor(id, paths[id])
		if err != nil {
			warnings = append(warnings, "Pier could not open "+capture.PathFor(paths[id])+" to capture requests: "+err.Error())
			continue
		}
		pc.keep = services
		if now.Sub(pc.pruned) >= pruneEvery {
			pc.prune(now)
		}
		if err, _ := pc.recorder.LastErr.Load().(error); err != nil {
			warnings = append(warnings, "Pier could not save captured requests: "+err.Error())
		}
	}

	routes := map[tapKey]localproxy.Route{}
	for key, tap := range wanted {
		route := localproxy.Route{Target: tap.Target, Service: tap.Service, Shaping: shapingOf(tap.Throttle)}
		if pc := d.captures[key.project]; pc != nil && tap.CaptureSeconds > 0 {
			route.Capture = pc.recorder
		}
		routes[key] = route
	}

	for key, server := range d.taps {
		if tap, ok := wanted[key]; !ok || tap.Port != server.port {
			server.close()
			delete(d.taps, key)
		}
	}
	statuses := make([]TapStatus, 0, len(wanted))
	for key, tap := range wanted {
		server := d.taps[key]
		if server == nil {
			server = d.openTap(tap.Port)
			d.taps[key] = server
		}
		if server.err == nil && (server.route != routes[key] || server.handler.Load() == nil) {
			if handler, err := d.proxy.Tap(routes[key]); err != nil {
				server.err = err
			} else {
				server.handler.Store(&handler)
				server.route = routes[key]
			}
		}
		status := TapStatus{Project: key.project, Service: key.service, Port: tap.Port}
		if server.err != nil {
			status.Error = server.err.Error()
			// A failed bind is retried next time, in case the port came free.
			server.close()
			delete(d.taps, key)
		}
		statuses = append(statuses, status)
	}
	sort.Slice(statuses, func(i, j int) bool {
		if statuses[i].Project != statuses[j].Project {
			return statuses[i].Project < statuses[j].Project
		}
		return statuses[i].Service < statuses[j].Service
	})
	return routes, statuses, warnings
}

func (d *daemon) openTap(port int) *tapServer {
	server := &tapServer{port: port}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		server.err = errors.New("tap port " + strconv.Itoa(port) + " is taken by another program; run pier down, then pier up, to pick another")
		return server
	}
	server.server = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if handler := server.handler.Load(); handler != nil {
				(*handler).ServeHTTP(w, r)
				return
			}
			http.Error(w, "Pier is still starting this service's tap", http.StatusServiceUnavailable)
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          quietLog(),
	}
	go func() { _ = server.server.Serve(listener) }()
	return server
}

func (s *tapServer) close() {
	if s.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = s.server.Shutdown(ctx)
}

func (d *daemon) captureFor(projectID, root string) (*projectCapture, error) {
	if pc := d.captures[projectID]; pc != nil {
		return pc, nil
	}
	path := capture.PathFor(root)
	store, err := capture.Open(path)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	pc := &projectCapture{path: path, store: store, recorder: capture.NewRecorder(store), stop: cancel, done: make(chan struct{})}
	go func() { pc.recorder.Run(ctx); close(pc.done) }()
	d.captures[projectID] = pc
	return pc, nil
}

// prune deletes captures past each service's keep time, and all captures of
// services that no longer capture.
func (pc *projectCapture) prune(now time.Time) {
	pc.pruned = now
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	services := make([]string, 0, len(pc.keep))
	for service, keep := range pc.keep {
		services = append(services, service)
		_, _ = pc.store.Prune(ctx, service, now.Add(-keep))
	}
	_ = pc.store.PruneExcept(ctx, services)
}

// close writes what is queued, then closes the file.
func (pc *projectCapture) close() {
	pc.stop()
	<-pc.done
	_ = pc.store.Close()
}

func (d *daemon) closeTaps() {
	for key, server := range d.taps {
		server.close()
		delete(d.taps, key)
	}
	for id, pc := range d.captures {
		pc.close()
		delete(d.captures, id)
	}
}

func shapingOf(t *state.Throttle) localproxy.Shaping {
	if t == nil {
		return localproxy.Shaping{}
	}
	return localproxy.Shaping{Latency: time.Duration(t.LatencyMS) * time.Millisecond, Down: t.Down, Up: t.Up}
}

// daemonWork reports whether some saved project needs the daemon besides its
// .local names: for taps, or to close a public window.
func daemonWork(saved []state.ProjectState) bool {
	for _, project := range saved {
		if project.Path != "" && (len(project.Taps) > 0 || project.OwnsTimedPublic()) {
			return true
		}
	}
	return false
}
