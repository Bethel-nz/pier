package localname

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"pier/internal/state"
)

// Run publishes saved local names until ctx is canceled.
// Each pass reads the machine's current address. A change stops the old
// advertisement before the new one starts, so a previous Wi-Fi address
// is not left published.
func Run(ctx context.Context, projects *state.Store, announce Announcer) error {
	publisher := NewPublisher(announce)
	defer func() { _ = publisher.StopAll() }()

	var address string
	var signature string
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
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
		if nextAddress != address || nextSignature != signature {
			if err := publisher.Reconcile(ctx, records, ip); err != nil {
				return err
			}
			if err := writeLease(publisher.PIDs()); err != nil {
				return err
			}
			address = nextAddress
			signature = nextSignature
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
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

func leasePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "pier", "locald.json"), nil
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
		_ = stopPID(pid)
	}
	return os.Remove(path)
}
