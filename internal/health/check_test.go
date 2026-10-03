package health

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
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

func healthServer(t *testing.T, code int) config.ResolvedService {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(server.Close)
	addr := server.Listener.Addr().(*net.TCPAddr)
	return config.ResolvedService{Name: "api", Host: addr.IP.String(), Port: uint16(addr.Port), Protocol: config.ProtocolHTTP, Health: "/healthz"}
}

func TestCheckReportsUnhealthyWhenHealthPathFails(t *testing.T) {
	result := Check(context.Background(), healthServer(t, http.StatusInternalServerError))

	if result.Status != StatusUnhealthy || result.Code != 500 {
		t.Fatalf("Check() = %+v, want unhealthy with code 500", result)
	}
	if result.Error != "GET /healthz returned 500" {
		t.Errorf("Check() error = %q", result.Error)
	}
}

func TestCheckAcceptsSuccessAndRedirectFromHealthPath(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusNoContent, http.StatusFound} {
		result := Check(context.Background(), healthServer(t, code))
		if result.Status != StatusHealthy || result.Code != code {
			t.Errorf("code %d: Check() = %+v, want healthy", code, result)
		}
	}
}

func TestCheckIgnoresHealthPathForTCPServices(t *testing.T) {
	service := healthServer(t, http.StatusInternalServerError)
	service.Protocol = config.ProtocolTCP

	if result := Check(context.Background(), service); result.Status != StatusHealthy {
		t.Fatalf("Check() = %+v, want healthy from the port alone", result)
	}
}
