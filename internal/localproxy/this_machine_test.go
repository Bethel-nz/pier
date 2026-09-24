package localproxy

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func requestFrom(remote, local string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "https://myapp.local/", nil)
	req.RemoteAddr = remote
	addr, _ := net.ResolveTCPAddr("tcp", local)
	return req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, addr))
}

func TestThisMachineOnlyRefusesOtherDevices(t *testing.T) {
	up := upstream(t)
	p := New()
	if err := p.SetRoutes([]Route{{Host: "myapp.local", Target: up.URL, Service: "web", ThisMachineOnly: true}}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, remote, local string
		want                int
	}{
		{"loopback", "127.0.0.1:5000", "127.0.0.1:443", http.StatusOK},
		{"this machine through its LAN address", "192.168.1.20:5000", "192.168.1.20:443", http.StatusOK},
		{"a phone on the LAN", "192.168.1.50:5000", "192.168.1.20:443", http.StatusForbidden},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		p.HTTPS().ServeHTTP(rec, requestFrom(tc.remote, tc.local))
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}
