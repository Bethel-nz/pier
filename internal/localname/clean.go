package localname

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/Bethel-nz/pier/internal/capture"
	"github.com/Bethel-nz/pier/internal/certs"
)

// CleanReport lists what pier clean removed and what it could not.
type CleanReport struct {
	StoppedDaemon bool     `json:"stoppedDaemon"`
	Untrusted     string   `json:"untrusted,omitempty"`
	RemovedCA     string   `json:"removedCA,omitempty"`
	RemovedCerts  []string `json:"removedCerts,omitempty"`
	// RemovedCaptures are capture files: requests kept for pier replay.
	RemovedCaptures []string `json:"removedCaptures,omitempty"`
	ClearedNames    []string `json:"clearedNames,omitempty"`
	// OrphanedDNS are Cloudflare hostnames no longer in any pier.yaml whose
	// DNS records still point at a Pier tunnel. Pier cannot delete them.
	OrphanedDNS []OrphanedDNS `json:"orphanedDNS,omitempty"`
	Errors      []string      `json:"errors,omitempty"`
}

// OrphanedDNS is a hostname still routed to a project's tunnel.
type OrphanedDNS struct {
	Hostname string `json:"hostname"`
	Tunnel   string `json:"tunnel"`
	Project  string `json:"project"`
}

// runtimeFiles are the daemon's files, plus the ones the first local-names
// prototype left behind.
var runtimeFiles = []string{
	"pierd.json", "pierd.log", "pierd.lock", "pierd.stop",
	"locald.json", "locald.pid", "locald.status", "locald.lock",
}

// Clean removes Pier's local-network footprint: it stops the daemon, takes
// the CA out of the trust store, deletes the CA and every project's
// certificate, and forgets saved local names so nothing is served until a
// project runs pier up again. Tailscale routes are not touched; pier down
// owns those. It keeps going after a failed step and reports each one.
func (d *Directory) Clean(ctx context.Context) CleanReport {
	var report CleanReport
	fail := func(step string, err error) {
		report.Errors = append(report.Errors, step+": "+err.Error())
	}

	if beat, err := readHeartbeat(); err == nil && beat.Fresh(d.now()) {
		if err := requestStop(beat.PID); err != nil {
			fail("stop the local-name daemon", err)
		} else {
			d.waitGone(ctx, beat.PID)
			report.StoppedDaemon = true
		}
	}
	sweepPublishers()

	saved, err := d.projects.List()
	if err != nil {
		fail("read projects", err)
	}
	for _, project := range saved {
		forgotOrphans := false
		if tunnel := project.Tunnel; tunnel != nil && len(tunnel.Orphans) > 0 {
			// Said here once, then forgotten: in a zone with a wildcard record a
			// deleted hostname still resolves, so Pier could never tell.
			gone := map[string]bool{}
			for _, host := range tunnel.Orphans {
				report.OrphanedDNS = append(report.OrphanedDNS, OrphanedDNS{Hostname: host, Tunnel: tunnel.Name, Project: project.Name})
				gone[host] = true
			}
			tunnel.Routed = slices.DeleteFunc(tunnel.Routed, func(host string) bool { return gone[host] })
			tunnel.Orphans = nil
			forgotOrphans = true
		}
		if project.Path != "" {
			dir := certs.LeafDir(project.Path)
			if _, statErr := os.Stat(dir); statErr == nil {
				if err := os.RemoveAll(dir); err != nil {
					fail("remove "+dir, err)
				} else {
					report.RemovedCerts = append(report.RemovedCerts, dir)
				}
			}
			db := capture.PathFor(project.Path)
			if _, statErr := os.Stat(db); statErr == nil {
				for _, file := range []string{db, db + "-wal", db + "-shm"} {
					if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
						fail("remove "+file, err)
					}
				}
				report.RemovedCaptures = append(report.RemovedCaptures, db)
			}
		}
		if len(project.Domains) == 0 {
			if forgotOrphans {
				if err := d.projects.Save(project); err != nil {
					fail("forget orphaned DNS records of "+project.Name, err)
				}
			}
			continue
		}
		for _, domain := range project.Domains {
			report.ClearedNames = append(report.ClearedNames, domain.Name)
		}
		project.Domains = nil
		if err := d.projects.Save(project); err != nil {
			fail("forget local names of "+project.Name, err)
		}
	}

	// Read the file directly: an expired CA must still come out of the trust store.
	caPath := filepath.Join(d.caDir, "ca.pem")
	if ca, err := certs.ReadCertPEM(caPath); err == nil {
		if err := d.untrust(ca, caPath); err != nil {
			// Keep the CA so a later pier clean can retry removing the same certificate.
			fail("remove Pier's CA from the trust store", err)
		} else {
			report.Untrusted = caPath
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		fail("read Pier's CA", err)
	}
	if report.Untrusted != "" || !exists(caPath) {
		if exists(d.caDir) {
			if err := os.RemoveAll(d.caDir); err != nil {
				fail("remove "+d.caDir, err)
			} else {
				report.RemovedCA = d.caDir
			}
		}
	}

	if err := d.setAutostart(false); err != nil {
		fail("remove the login item", err)
	}

	for _, name := range runtimeFiles {
		path, err := runtimePath(name)
		if err != nil {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			fail(fmt.Sprintf("remove %s", path), err)
		}
	}
	return report
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
