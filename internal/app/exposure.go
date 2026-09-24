package app

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Bethel-nz/pier/internal/config"
	"github.com/Bethel-nz/pier/internal/reconcile"
	"github.com/Bethel-nz/pier/internal/state"
)

// stalePublic is how long a public route may live before pier doctor asks
// whether it is still wanted.
const stalePublic = 24 * time.Hour

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// withLiveRoutes replaces what pier.yaml says with what Tailscale serves:
// a URL only for a route that exists, Public only for a live Funnel route,
// and Drift for every difference, with the command that fixes it.
func withLiveRoutes(infos []ServiceInfo, services []config.ResolvedService, actual []reconcile.Route, owned []state.Route, dns string, paused map[string]bool) {
	live := make(map[string]reconcile.Route, len(actual))
	for _, route := range actual {
		live[route.Key()] = route
	}
	for i, service := range services {
		info := &infos[i]
		info.URL = ""
		info.Public = false
		if !service.OnTailscale() {
			continue // served by Cloudflare; pier up removes any route it had
		}
		want := reconcile.Route{HTTPSPort: service.HTTPSPort, Path: service.Path, TCP: service.TCP()}
		if route, ok := live[want.Key()]; ok {
			info.URL = routeURL(dns, route)
			info.Public = route.Public
			if !ownedKey(owned, service.Name, want.Key()) {
				info.Drift = append(info.Drift, "served by a route this project did not create; run pier up --force to adopt it")
			}
			if route.Target != service.Target {
				info.Drift = append(info.Drift, fmt.Sprintf("Tailscale sends it to %s, not %s; run pier up", shortTarget(route.Target), shortTarget(service.Target)))
			}
			if route.Public != service.Public {
				info.Drift = append(info.Drift, publicDrift(route.Public))
			}
			if paused[service.Name] {
				info.Drift = append(info.Drift, "paused, but Tailscale still serves it; run pier pause "+service.Name+" again")
			}
		} else if !paused[service.Name] {
			info.Drift = append(info.Drift, "not served on Tailscale; run pier up")
		}
		// Routes this project made earlier, somewhere pier.yaml no longer asks for.
		for _, old := range owned {
			key := ownedRoute(old).Key()
			route, ok := live[key]
			if old.Service != service.Name || key == want.Key() || !ok {
				continue
			}
			if route.Public {
				info.Public = true
			}
			info.Drift = append(info.Drift, fmt.Sprintf("an older route at %s is still live; run pier up to remove it", routeURL(dns, route)))
		}
		for _, old := range owned {
			if old.Service == service.Name && old.Public && !old.Since.IsZero() {
				if route, ok := live[ownedRoute(old).Key()]; ok && route.Public {
					info.PublicSince = old.Since
				}
			}
		}
	}
}

// routeURL is where a route is reached: an https:// URL, or tcp://host:port.
func routeURL(dns string, route reconcile.Route) string {
	if route.TCP {
		if dns = strings.TrimSuffix(dns, "."); dns == "" {
			return ""
		}
		return "tcp://" + net.JoinHostPort(dns, strconv.Itoa(int(route.HTTPSPort)))
	}
	return serviceURL(dns, route.HTTPSPort, route.Path)
}

func ownedKey(owned []state.Route, service, key string) bool {
	for _, route := range owned {
		if route.Service == service && ownedRoute(route).Key() == key {
			return true
		}
	}
	return false
}

func publicDrift(livePublic bool) string {
	if livePublic {
		return "PUBLIC on the internet, but pier.yaml says private; run pier up"
	}
	return "private on Tailscale, but pier.yaml says public; run pier up"
}

// shortTarget is a target as people read it: 127.0.0.1:3000.
func shortTarget(target string) string {
	if parsed, err := url.Parse(target); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return target
}

// MachineRoute is one route Tailscale serves on this machine.
type MachineRoute struct {
	Route reconcile.Route
	URL   string
	// Project and Service name the Pier project that owns it; empty when none does.
	Project string
	Service string
	Since   time.Time
}

// MachineResult is every route on this machine, public ones first.
type MachineResult struct {
	DNSName string
	Routes  []MachineRoute
}

// Machine lists every Tailscale Serve and Funnel route on this machine,
// whichever project made it, or none.
func (s *Service) Machine(ctx context.Context) (MachineResult, error) {
	if _, err := s.ts.Check(ctx); err != nil {
		return MachineResult{}, &PrerequisiteError{Err: err}
	}
	actual, err := s.ts.Routes(ctx)
	if err != nil {
		return MachineResult{}, fmt.Errorf("Pier could not read Tailscale routes: %w", err)
	}
	dns := s.lookupDNS(ctx, "")
	owners := s.owners()
	result := MachineResult{DNSName: dns}
	for _, route := range actual {
		entry := MachineRoute{Route: route, URL: routeURL(dns, route)}
		if owner, ok := owners[route.Key()]; ok {
			entry.Project, entry.Service, entry.Since = owner.project, owner.route.Service, owner.route.Since
		}
		result.Routes = append(result.Routes, entry)
	}
	sort.SliceStable(result.Routes, func(i, j int) bool {
		a, b := result.Routes[i].Route, result.Routes[j].Route
		if a.Public != b.Public {
			return a.Public
		}
		return a.Key() < b.Key()
	})
	return result, nil
}

type owner struct {
	project string
	route   state.Route
}

// owners maps each route key to the saved Pier project that owns it.
func (s *Service) owners() map[string]owner {
	out := map[string]owner{}
	lister, ok := s.store.(interface {
		List() ([]state.ProjectState, error)
	})
	if !ok {
		return out
	}
	projects, err := lister.List()
	if err != nil {
		return out
	}
	for _, project := range projects {
		name := project.Name
		if name == "" {
			name = project.ProjectID
		}
		for _, route := range project.Routes {
			out[ownedRoute(route).Key()] = owner{project: name, route: route}
		}
	}
	return out
}

// publicWarnings finds public routes worth a second look: ones no Pier
// project owns, and ones public for longer than stalePublic. Routes in skip
// are left out.
func publicWarnings(actual []reconcile.Route, owners map[string]owner, dns string, now time.Time, skip map[string]bool) []string {
	var warnings []string
	for _, route := range actual {
		if !route.Public || skip[route.Key()] {
			continue
		}
		url := routeURL(dns, route)
		if url == "" {
			url = route.Key()
		}
		o, ok := owners[route.Key()]
		switch {
		case !ok:
			warnings = append(warnings, fmt.Sprintf("%s is PUBLIC on the internet and no Pier project owns it; remove it with tailscale funnel, or adopt it with pier up --force", url))
		case !o.route.Since.IsZero() && now.Sub(o.route.Since) > stalePublic:
			warnings = append(warnings, fmt.Sprintf("%s (%s in %s) has been PUBLIC for %s; run pier unshare %s or pier down if it is no longer needed", url, o.route.Service, o.project, Age(now.Sub(o.route.Since)), o.route.Service))
		}
	}
	return warnings
}

// Age is a duration as people say it: 3d, 5h, 12m.
func Age(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return "under a minute"
	}
}

// pathMismatch explains when `pier` on PATH is not the binary running, the
// usual cause of "I installed the fix but nothing changed".
var pathMismatch = func() string {
	onPath, err := exec.LookPath("pier")
	if err != nil {
		return "pier is not on your PATH; add $(go env GOPATH)/bin to PATH"
	}
	running, err := os.Executable()
	if err != nil {
		return ""
	}
	a, errA := os.Stat(onPath)
	b, errB := os.Stat(running)
	if errA != nil || errB != nil || os.SameFile(a, b) {
		return ""
	}
	return fmt.Sprintf("the pier on your PATH (%s) is not the one running (%s); after go install, put $(go env GOPATH)/bin first in PATH", onPath, running)
}
