//go:build darwin

package localname

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// instancePrefix marks Pier's dns-sd registrations so a sweep can find them
// after a crash, even though no Pier process remembers their pids.
const instancePrefix = "pier-"

// dnssd registers names with macOS's mDNSResponder through `dns-sd -P`.
// Each registration lives as long as its dns-sd process.
type dnssd struct{}

type dnssdHandle struct {
	cmd     *exec.Cmd
	started time.Time
	done    chan struct{}
}

func systemBackend() backend {
	if _, err := exec.LookPath("dns-sd"); err != nil {
		return nil
	}
	return dnssd{}
}

func (dnssd) kind() string { return "mDNSResponder" }

func (dnssd) register(name, address string, port int) (handle, error) {
	label := strings.TrimSuffix(name, ".local")
	cmd := exec.Command("dns-sd", "-P", instancePrefix+label, "_http._tcp", "local",
		strconv.Itoa(port), name, address)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	h := &dnssdHandle{cmd: cmd, started: time.Now(), done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(h.done) }()
	return h, nil
}

// live holds once dns-sd has kept running for a moment, since it exits as soon
// as mDNSResponder rejects the record.
func (h *dnssdHandle) live() bool { return time.Since(h.started) > 500*time.Millisecond }

func (h *dnssdHandle) exited() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

func (h *dnssdHandle) stop() {
	_ = h.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-h.done:
	case <-time.After(time.Second):
		_ = h.cmd.Process.Kill()
	}
}

// sweepPublishers stops dns-sd registrations a previous Pier left behind,
// found by the instance-name marker. The daemon runs it when it starts, and
// pier down runs it when nothing is left to serve.
func sweepPublishers() {
	out, err := exec.Command("ps", "-Ao", "pid=,command=").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasSuffix(fields[1], "dns-sd") || fields[2] != "-P" || !strings.HasPrefix(fields[3], instancePrefix) {
			continue
		}
		if pid, err := strconv.Atoi(fields[0]); err == nil {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	}
}
