package localproxy

import (
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Bethel-nz/pier/internal/certs"
)

func testCA(t *testing.T) []byte {
	t.Helper()
	ca, _, err := certs.LoadOrCreateCA(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(ca.CertPath())
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func get(h http.Handler, url, userAgent string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", userAgent)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const iPhone = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile Safari/604.1"

func TestInstallPageLeadsWithTheVisitorsPlatform(t *testing.T) {
	p := newProxy(t, upstream(t).URL)
	p.SetCA(testCA(t))

	for _, handler := range []http.Handler{p.HTTP(), p.HTTPS()} {
		rec := get(handler, "http://my-app.local/.pier/", iPhone)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("install page = %d", rec.Code)
		}
		if !strings.Contains(body, "<details open><summary>iPhone and iPad") {
			t.Fatal("iPhone steps are not first and open")
		}
		if !strings.Contains(body, p.ca.fingerprint) || !strings.Contains(body, `href="https://my-app.local/"`) {
			t.Fatal("page is missing the fingerprint or the continue link")
		}
	}

	windows := get(p.HTTP(), "http://192.168.1.20/.pier", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)").Body.String()
	if !strings.Contains(windows, "<details open><summary>Windows") || strings.Contains(windows, "Continue to") {
		t.Fatal("bare-IP Windows visit should open Windows steps and offer no continue link")
	}
}

func TestInstallDownloads(t *testing.T) {
	p := New()
	pemBytes := testCA(t)
	p.SetCA(pemBytes)

	crt := get(p.HTTP(), "http://my-app.local"+crtPath, "")
	if _, err := x509.ParseCertificate(crt.Body.Bytes()); err != nil || crt.Header().Get("Content-Type") != "application/x-x509-ca-cert" {
		t.Fatalf(".crt is not a DER certificate: %v %q", err, crt.Header().Get("Content-Type"))
	}

	profile := get(p.HTTP(), "http://my-app.local"+profilePath, iPhone)
	body := profile.Body.String()
	if profile.Header().Get("Content-Type") != "application/x-apple-aspen-config" ||
		!strings.Contains(body, "com.apple.security.root") || !strings.Contains(body, "<data>") {
		t.Fatalf("profile = %q %q", profile.Header().Get("Content-Type"), body)
	}
	p.SetCA(pemBytes)
	if again := get(p.HTTP(), "http://my-app.local"+profilePath, iPhone).Body.String(); again != body {
		t.Fatal("the same CA should produce the same profile, so reinstalling replaces it")
	}
}

func TestInstallPageWithoutCA(t *testing.T) {
	rec := get(New().HTTP(), "http://my-app.local/.pier/", "")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "pier up") {
		t.Fatalf("no-CA page = %d", rec.Code)
	}
}
