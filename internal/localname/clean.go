package localname

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	Errors          []string `json:"errors,omitempty"`
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
