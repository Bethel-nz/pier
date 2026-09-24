package localproxy

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func upstream(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen-Host", r.Host)
		w.Header().Set("X-Seen-Forwarded-Host", r.Header.Get("X-Forwarded-Host"))
		w.Header().Set("X-Seen-Forwarded-Proto", r.Header.Get("X-Forwarded-Proto"))
		w.Header().Set("X-Seen-Hops", r.Header.Get(hopHeader))
		_, _ = io.WriteString(w, "hello from "+r.URL.Path)
	}))
	t.Cleanup(server.Close)
	return server
}

func newProxy(t *testing.T, target string) *Proxy {
	t.Helper()
	p := New()
	if err := p.SetRoutes([]Route{{Host: "My-App.local", Target: target, Service: "web"}}); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRoutesByHostAndRewritesHeaders(t *testing.T) {
	up := upstream(t)
	p := newProxy(t, up.URL)
	req := httptest.NewRequest(http.MethodGet, "https://my-app.local/dash?x=1", nil)
	req.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	p.HTTPS().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "hello from /dash" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Seen-Host"); got != strings.TrimPrefix(up.URL, "http://") {
		t.Fatalf("upstream Host = %q, want the target so dev-server host checks pass", got)
	}
	if got := rec.Header().Get("X-Seen-Forwarded-Host"); got != "my-app.local" {
		t.Fatalf("X-Forwarded-Host = %q", got)
	}
	if got := rec.Header().Get("X-Seen-Forwarded-Proto"); got != "https" {
		t.Fatalf("X-Forwarded-Proto = %q", got)
	}
	if got := rec.Header().Get("X-Seen-Hops"); got != "1" {
		t.Fatalf("hops = %q", got)
	}
}

func TestUnknownHostAndBareIPAreRefused(t *testing.T) {
	p := newProxy(t, upstream(t).URL)
	for _, host := range []string{"other.local", "192.168.1.20", "192.168.1.20:443"} {
		req := httptest.NewRequest(http.MethodGet, "https://"+host+"/", nil)
		rec := httptest.NewRecorder()
		p.HTTPS().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", host, rec.Code)
		}
	}
}

func TestRouteListIsShownOnlyToThisMachine(t *testing.T) {
	p := newProxy(t, upstream(t).URL)
	local := httptest.NewRequest(http.MethodGet, "https://nope.local/", nil)
	local.RemoteAddr = "127.0.0.1:5000"
	rec := httptest.NewRecorder()
	p.HTTPS().ServeHTTP(rec, local)
	if !strings.Contains(rec.Body.String(), "my-app.local") {
		t.Fatal("loopback client should see the route list")
	}
	remote := httptest.NewRequest(http.MethodGet, "https://nope.local/", nil)
	remote.RemoteAddr = "192.168.1.50:5000"
	rec = httptest.NewRecorder()
	p.HTTPS().ServeHTTP(rec, remote)
	if strings.Contains(rec.Body.String(), "my-app.local") {
		t.Fatal("LAN client must not see the route list")
	}
}

func TestLoopIsDetected(t *testing.T) {
	p := newProxy(t, upstream(t).URL)
	req := httptest.NewRequest(http.MethodGet, "https://my-app.local/", nil)
	req.Header.Set(hopHeader, "5")
	rec := httptest.NewRecorder()
	p.HTTPS().ServeHTTP(rec, req)
	if rec.Code != http.StatusLoopDetected || !strings.Contains(rec.Body.String(), "changeOrigin") {
		t.Fatalf("status = %d, want 508 with a changeOrigin hint", rec.Code)
	}
}

func TestStoppedServiceGets502(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	p := newProxy(t, "http://"+addr)
	rec := httptest.NewRecorder()
	p.HTTPS().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://my-app.local/", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "web is not responding") {
		t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
	}
}

func TestHTTPRedirectsAndServesCA(t *testing.T) {
	p := newProxy(t, upstream(t).URL)
	p.SetCA([]byte("-----BEGIN CERTIFICATE-----\n"))

	rec := httptest.NewRecorder()
	p.HTTP().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://my-app.local/a?b=1", nil))
	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "https://my-app.local/a?b=1" {
		t.Fatalf("redirect = %d %q", rec.Code, rec.Header().Get("Location"))
	}

	p.SetHTTPSPort(8443)
	rec = httptest.NewRecorder()
	p.HTTP().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://my-app.local/", nil))
	if rec.Header().Get("Location") != "https://my-app.local:8443/" {
		t.Fatalf("fallback-port redirect = %q", rec.Header().Get("Location"))
	}

	rec = httptest.NewRecorder()
	p.HTTP().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://192.168.1.20"+CAPath, nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-x509-ca-cert" {
		t.Fatalf("CA download = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestCertificateIsChosenBySNI(t *testing.T) {
	p := New()
	cert := &tls.Certificate{}
	p.SetCertificates(map[string]*tls.Certificate{"My-App.local": cert})
	got, err := p.getCertificate(&tls.ClientHelloInfo{ServerName: "my-app.local"})
	if err != nil || got != cert {
		t.Fatalf("getCertificate = %v, %v", got, err)
	}
	if _, err := p.getCertificate(&tls.ClientHelloInfo{ServerName: "other.local"}); err == nil {
		t.Fatal("unknown SNI must fail the handshake")
	}
}

func TestWebSocketUpgradePassesThrough(t *testing.T) {
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "want upgrade", http.StatusBadRequest)
			return
		}
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = buf.Flush()
		line, _ := buf.ReadString('\n')
		_, _ = buf.WriteString("echo " + line)
		_ = buf.Flush()
	}))
	defer echo.Close()

	p := newProxy(t, echo.URL)
	front := httptest.NewServer(p.HTTPS())
	defer front.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(front.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "GET /hmr HTTP/1.1\r\nHost: my-app.local\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	_, _ = io.WriteString(conn, "ping\n")
	line, err := reader.ReadString('\n')
	if err != nil || line != "echo ping\n" {
		t.Fatalf("echo = %q, %v", line, err)
	}
}

func TestListenFallsBackToTheNextPort(t *testing.T) {
	busy, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port
	free, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	freePort := free.Addr().(*net.TCPAddr).Port
	free.Close()

	listener, port, err := Listen([]int{busyPort, freePort})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if port != freePort {
		t.Fatalf("port = %d, want fallback %d", port, freePort)
	}
}

func TestOriginIsTranslatedOnlyForSameOriginReads(t *testing.T) {
	seen := make(chan string, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Origin")
	}))
	defer up.Close()
	p := newProxy(t, up.URL)
	upstreamOrigin := "http://" + strings.TrimPrefix(up.URL, "http://")

	cases := []struct {
		name, method, origin, want string
	}{
		{"same-origin GET (dev assets, HMR socket)", http.MethodGet, "https://my-app.local", upstreamOrigin},
		{"same-origin POST keeps the public origin for CSRF checks", http.MethodPost, "https://my-app.local", "https://my-app.local"},
		{"foreign origin is never rewritten", http.MethodGet, "https://evil.example", "https://evil.example"},
		{"plain-HTTP look-alike is not same-origin", http.MethodGet, "http://my-app.local", "http://my-app.local"},
		{"no origin stays absent", http.MethodGet, "", ""},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, "https://my-app.local/_next/static/chunk.js", nil)
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		p.HTTPS().ServeHTTP(httptest.NewRecorder(), req)
		if got := <-seen; got != tc.want {
			t.Errorf("%s: upstream Origin = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestGRPCReachesAnH2CUpstreamWithTrailers(t *testing.T) {
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status")
		w.Header().Set("X-Proto", r.Proto)
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Grpc-Status", "0")
	}))
	up.Config.Protocols = new(http.Protocols)
	up.Config.Protocols.SetHTTP1(true)
	up.Config.Protocols.SetUnencryptedHTTP2(true)
	up.Start()
	defer up.Close()

	p := newProxy(t, up.URL)
	front := httptest.NewUnstartedServer(p.HTTPS())
	front.EnableHTTP2 = true
	front.StartTLS()
	defer front.Close()

	req, err := http.NewRequest(http.MethodPost, front.URL+"/grpc.health.v1.Health/Check", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "my-app.local"
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	resp, err := front.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := resp.Header.Get("X-Proto"); got != "HTTP/2.0" {
		t.Fatalf("upstream saw %s, want HTTP/2.0 (h2c) for gRPC", got)
	}
	if got := resp.Trailer.Get("Grpc-Status"); got != "0" {
		t.Fatalf("grpc-status trailer = %q, want 0", got)
	}
}
