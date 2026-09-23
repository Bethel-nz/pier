package localname

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Bethel-nz/pier/internal/state"
)

// Run publishes saved local names until ctx is canceled.
// Each pass reads the machine's current address. A change stops the old
// advertisement before the new one starts, so a previous Wi-Fi address
// is not left published.
func Run(ctx context.Context, projects *state.Store, announce Announcer) error {
	lock, owned, err := lockDaemon()
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	defer lock.Close()
	if path, pathErr := statusPath(); pathErr == nil {
		_ = os.Remove(path)
	}

	publisher := NewPublisher(announce)
	defer func() {
		_ = publisher.StopAll()
		if path, pathErr := pidPath(); pathErr == nil {
			_ = os.Remove(path)
		}
		if path, pathErr := statusPath(); pathErr == nil {
			_ = os.Remove(path)
		}
	}()

	var address string
	var signature string
	started := false
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		if err := publishOnce(ctx, projects, publisher, &address, &signature); err != nil {
			_ = writeStatus("error: " + err.Error())
			if !started {
				return err
			}
		} else {
			started = true
			_ = writeStatus("ok")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func publishOnce(ctx context.Context, projects *state.Store, publisher *Publisher, address, signature *string) error {
	projectsNow, err := projects.List()
	if err != nil {
		return err
	}
	records, err := Records(projectsNow)
	if err != nil {
		return err
	}
	ip, err := CurrentIPv4()
	if err != nil {
		return err
	}
	nextAddress := ""
	if ip != nil {
		nextAddress = ip.String()
	}
	nextSignature := signatureOf(records)
	if nextAddress == *address && nextSignature == *signature {
		return nil
	}
	if err := publisher.Reconcile(ctx, records, ip); err != nil {
		return err
	}
	if err := writeLease(publisher.PIDs()); err != nil {
		return err
	}
	*address = nextAddress
	*signature = nextSignature
	return nil
}

func signatureOf(records []Record) string {
	parts := make([]string, len(records))
	for i, record := range records {
		parts[i] = fmt.Sprintf("%s:%d", record.Name, record.Port)
	}
	return stringsJoin(parts)
}

func stringsJoin(parts []string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += ","
		}
		out += part
	}
	return out
}

type leaseFile struct {
	PIDs []int `json:"pids"`
}

func runtimePath(name string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "pier", name), nil
}

func leasePath() (string, error)  { return runtimePath("locald.json") }
func statusPath() (string, error) { return runtimePath("locald.status") }
func pidPath() (string, error)    { return runtimePath("locald.pid") }
func lockPath() (string, error)   { return runtimePath("locald.lock") }

func writeStatus(message string) error {
	path, err := statusPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(message+"\n"), 0o600)
}

// WaitReady blocks until the publisher reports its first result.
func WaitReady() error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		if path, err := statusPath(); err == nil {
			contents, readErr := os.ReadFile(path)
			if readErr == nil {
				text := strings.TrimSpace(string(contents))
				switch {
				case text == "ok":
					return nil
				case strings.HasPrefix(text, "error:"):
					_ = stopDaemon()
					return fmt.Errorf("Pier could not publish local names: %s", strings.TrimPrefix(text, "error:"))
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Pier could not confirm local name publishing")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func writeLease(pids []int) error {
	path, err := leasePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.Marshal(leaseFile{PIDs: pids})
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o600)
}

// Release stops advertisement processes recorded by a previous publisher.
func Release() error {
	path, err := leasePath()
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var lease leaseFile
	if err := json.Unmarshal(contents, &lease); err != nil {
		return err
	}
	for _, pid := range lease.PIDs {
		if !isAdvertiser(pid) {
			continue
		}
		_ = stopPID(pid)
	}
	return os.Remove(path)
}
