package health

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Bethel-nz/pier/internal/config"
)

func TestCheckReportsHealthyForOpenPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().(*net.TCPAddr)
	result := Check(context.Background(), config.ResolvedService{
		Name: "web",
		Host: addr.IP.String(),
		Port: uint16(addr.Port),
	})

	if result.Service != "web" {
		t.Errorf("Check() service = %q, want web", result.Service)
	}
	if result.Status != StatusHealthy {
		t.Errorf("Check() status = %q, want %q", result.Status, StatusHealthy)
	}
	if result.Error != "" {
		t.Errorf("Check() error = %q, want empty", result.Error)
	}
}

func TestCheckReportsUnavailableForClosedPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().(*net.TCPAddr)
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	result := Check(context.Background(), config.ResolvedService{
		Name: "api",
		Host: addr.IP.String(),
		Port: uint16(addr.Port),
	})

	if result.Status != StatusUnavailable {
		t.Errorf("Check() status = %q, want %q", result.Status, StatusUnavailable)
	}
	if result.Error == "" {
		t.Error("Check() error is empty, want a connection failure")
	}
}

func TestCheckDefaultTimeout(t *testing.T) {
	if DefaultTimeout != 500*time.Millisecond {
		t.Fatalf("DefaultTimeout = %v, want 500ms", DefaultTimeout)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	result := Check(ctx, config.ResolvedService{
		Name: "blackhole",
		Host: "192.0.2.1",
		Port: 81,
	})
	elapsed := time.Since(start)

	if result.Status != StatusUnavailable {
		t.Errorf("Check() status = %q, want %q", result.Status, StatusUnavailable)
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("Check() took %v, want the 500ms default timeout", elapsed)
	}
}

func TestCheckHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	result := Check(ctx, config.ResolvedService{
		Name: "web",
		Host: "192.0.2.1",
		Port: 81,
	})
	elapsed := time.Since(start)

	if result.Status != StatusUnavailable {
		t.Errorf("Check() status = %q, want %q", result.Status, StatusUnavailable)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("Check() took %v after cancel, want immediate return", elapsed)
	}
}
