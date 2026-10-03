package localname

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bethel-nz/pier/internal/state"
)

func TestMergeKeepsEachURLsHistory(t *testing.T) {
	var record Supervision
	start := time.Date(2026, 10, 3, 14, 0, 0, 0, time.UTC)
	url := "https://host.ts.net/hooks"

	record.merge(SupervisePass{Probes: []Probe{{Service: "hook", URL: url, Status: 200}}}, nil, start, 0)
	record.merge(SupervisePass{Probes: []Probe{{Service: "hook", URL: url, Error: "answered 502 Bad Gateway"}}}, nil, start.Add(time.Minute), 0)
	record.merge(SupervisePass{Probes: []Probe{{Service: "hook", URL: url, Error: "answered 502 Bad Gateway"}}}, nil, start.Add(2*time.Minute), 0)

	probe := record.Probes[0]
	if !probe.FailingSince.Equal(start.Add(time.Minute)) || !probe.LastSuccess.Equal(start) {
		t.Fatalf("probe = %+v, want failing since the first failure and the last success kept", probe)
	}

	record.merge(SupervisePass{Repaired: []string{"hook"}, Probes: []Probe{{Service: "hook", URL: url, Status: 200}}}, nil, start.Add(3*time.Minute), 0)
	probe = record.Probes[0]
	if !probe.FailingSince.IsZero() || probe.Error != "" || record.Repairs != 1 || !record.Repaired["hook"].Equal(start.Add(3*time.Minute)) {
		t.Fatalf("record = %+v, want healed and the repair counted", record)
	}
}

func TestMergeBacksOffWhileChecksFail(t *testing.T) {
	var record Supervision
	now := time.Date(2026, 10, 3, 14, 0, 0, 0, time.UTC)
	failures := 0
	var waits []time.Duration
	for i := 0; i < 6; i++ {
		failures = record.merge(SupervisePass{}, errors.New("Tailscale is not running"), now, failures)
		waits = append(waits, record.NextCheck.Sub(now))
	}
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("waits = %v, want %v", waits, want)
		}
	}
	if !record.ErrorSince.Equal(now) || len(superviseWarnings("demo", record)) != 1 {
		t.Fatalf("record = %+v, want one warning since the first failure", record)
	}

	failures = record.merge(SupervisePass{}, nil, now, failures)
	if failures != 0 || record.Error != "" || record.NextCheck.Sub(now) != superviseEvery {
		t.Fatalf("after recovery: failures=%d record=%+v", failures, record)
	}
}

func TestSuperviseAllChecksEachProjectOnSchedule(t *testing.T) {
	var calls atomic.Int32
	d := &daemon{
		ctx:      context.Background(),
		expiring: map[string]*expiry{},
		watches:  map[string]*watch{},
		supervise: func(context.Context, string) (SupervisePass, error) {
			calls.Add(1)
			return SupervisePass{}, nil
		},
	}
	saved := []state.ProjectState{
		{ProjectID: "p1", Name: "demo", Path: "/tmp/demo", Routes: []state.Route{{Service: "web", HTTPSPort: 8443, Path: "/"}}},
		{ProjectID: "p2", Name: "idle", Path: "/tmp/idle"},
	}
	now := time.Now()
	if _, _, active := d.superviseAll(saved, now); !active {
		t.Fatal("a project with routes should keep the daemon running")
	}
	deadline := time.Now().Add(2 * time.Second)
	running := func() bool {
		w := d.watches["p1"]
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.running
	}
	for calls.Load() == 0 || running() {
		if time.Now().After(deadline) {
			t.Fatal("the check never ran")
		}
		time.Sleep(10 * time.Millisecond)
	}
	d.superviseAll(saved, time.Now()) // not due for another 30s
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("checks = %d, want 1 until the next interval", calls.Load())
	}
	if _, ok := d.watches["p2"]; ok {
		t.Fatal("a project with nothing to watch was checked")
	}
	if _, _, active := d.superviseAll(saved[1:], time.Now()); active {
		t.Fatal("nothing to watch should let the daemon exit")
	}
}

func TestBusyPassKeepsTheRecordAndRetriesSoon(t *testing.T) {
	now := time.Date(2026, 10, 3, 14, 0, 0, 0, time.UTC)
	record := Supervision{Probes: []Probe{{URL: "https://host.ts.net/", LastSuccess: now}}}
	failures := record.merge(SupervisePass{Busy: true}, nil, now, 2)
	if failures != 2 || len(record.Probes) != 1 || record.NextCheck.Sub(now) != busyRetry {
		t.Fatalf("record = %+v failures=%d, want it kept and a retry in %v", record, failures, busyRetry)
	}
}
