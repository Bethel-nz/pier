package localname

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Bethel-nz/pier/internal/state"
)

// Status is the dashboard's view of everything Pier serves on this machine.
type Status struct {
	Daemon   DaemonStatus    `json:"daemon"`
	Names    []NameStatus    `json:"names"`
	Projects []ProjectStatus `json:"projects"`
}

// DaemonStatus describes the running daemon.
type DaemonStatus struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt"`
	HTTPSPort int       `json:"httpsPort"`
	HTTPPort  int       `json:"httpPort,omitempty"`
	MDNSError string    `json:"mdnsError,omitempty"`
	Warnings  []string  `json:"warnings,omitempty"`
}

// ProjectStatus is one saved Pier project.
type ProjectStatus struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Path     string              `json:"path"`
	Settings state.LocalSettings `json:"settings"`
	Names    []ProjectName       `json:"names"`
	Paused   []string            `json:"paused,omitempty"`
}

// ProjectName is one served name and the service behind it.
type ProjectName struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	Target  string `json:"target"`
	URL     string `json:"url"`
}

// api is the loopback-only control surface a dashboard builds on.
type api struct {
	d     *daemon
	port  int
	pier  string // this binary, for actions that run the real CLI
	actor func(ctx context.Context, exe string, args ...string) ([]byte, error)
}

func (d *daemon) serveAPI() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	exe, _ := os.Executable()
	a := &api{d: d, port: listener.Addr().(*net.TCPAddr).Port, pier: exe, actor: runPier}
	server := &http.Server{Handler: a.handler(), ReadHeaderTimeout: 10 * time.Second, ErrorLog: quietLog()}
	go func() { _ = server.Serve(listener) }()
	d.servers = append(d.servers, server)
	return a.port, nil
}

func (a *api) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("GET /api/requests", a.requests)
	mux.HandleFunc("GET /api/events", a.events)
	mux.HandleFunc("POST /api/projects/{project}/services/{service}/{action}", a.action)
	return a.guard(mux)
}

// guard keeps web pages out. Only a loopback Host is served (a DNS-rebinding
// page arrives with its own Host), and state changes need an X-Pier header,
// which a cross-site page cannot send without a CORS preflight Pier never answers.
func (a *api) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, err := net.SplitHostPort(r.Host)
		if err != nil || port != strconv.Itoa(a.port) || (host != "127.0.0.1" && host != "localhost") {
			http.Error(w, "Pier's API only answers on this machine's loopback address", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-Pier") == "" {
			http.Error(w, "missing X-Pier header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *api) snapshot() Status {
	beat := a.d.heartbeat()
	status := Status{
		Daemon: DaemonStatus{
			PID: beat.PID, StartedAt: beat.StartedAt, HTTPSPort: beat.HTTPSPort, HTTPPort: beat.HTTPPort,
			MDNSError: beat.MDNSError, Warnings: beat.Warnings,
		},
		Names:    beat.Names,
		Projects: []ProjectStatus{},
	}
	saved, err := a.d.projects.List()
	if err != nil {
		status.Daemon.Warnings = append(status.Daemon.Warnings, "Pier could not read project state: "+err.Error())
		return status
	}
	for _, project := range saved {
		entry := ProjectStatus{ID: project.ProjectID, Name: project.Name, Path: project.Path, Settings: project.Local, Names: []ProjectName{}}
		for _, domain := range project.Domains {
			entry.Names = append(entry.Names, ProjectName{
				Service: domain.Service, Name: domain.Name, Target: domain.Target, URL: beat.URL(domain.Name),
			})
		}
		for service, paused := range project.Paused {
			if paused {
				entry.Paused = append(entry.Paused, service)
			}
		}
		status.Projects = append(status.Projects, entry)
	}
	return status
}

func (a *api) status(w http.ResponseWriter, _ *http.Request) {
	writeJSONResponse(w, http.StatusOK, a.snapshot())
}

func (a *api) requests(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 || limit > 500 {
		limit = 100
	}
	writeJSONResponse(w, http.StatusOK, a.d.proxy.Recent(r.URL.Query().Get("host"), limit))
}

// events streams `status` when served names change and `request` for each
// proxied request, as server-sent events.
func (a *api) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	live, cancel := a.d.proxy.Subscribe()
	defer cancel()
	send := func(event string, value any) {
		payload, _ := json.Marshal(value)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
		flusher.Flush()
	}
	last := ""
	sendStatus := func() {
		status := a.snapshot()
		key, _ := json.Marshal(struct {
			Names    []NameStatus
			Projects []ProjectStatus
			Warnings []string
		}{status.Names, status.Projects, status.Daemon.Warnings})
		if string(key) != last {
			last = string(key)
			send("status", status)
		}
	}
	sendStatus()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case req := <-live:
			send("request", req)
		case <-tick.C:
			sendStatus()
		}
	}
}

// action runs the real CLI command, so the dashboard and pier behave the same.
func (a *api) action(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action != "pause" && action != "resume" {
		http.Error(w, "action must be pause or resume", http.StatusNotFound)
		return
	}
	saved, err := a.d.projects.Load(r.PathValue("project"))
	if err != nil || saved.Path == "" {
		http.Error(w, "unknown project", http.StatusNotFound)
		return
	}
	service := r.PathValue("service")
	if strings.ContainsAny(service, " /\\") || strings.HasPrefix(service, "-") {
		http.Error(w, "invalid service name", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := a.actor(ctx, a.pier, "--json", "--config", saved.Path, action, service)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	_, _ = w.Write(out)
}

func runPier(ctx context.Context, exe string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, exe, args...).Output()
}

func writeJSONResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
