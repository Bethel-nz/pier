package localproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Bethel-nz/pier/internal/config"
)

func TestSetupHostIsTheReservedName(t *testing.T) {
	if SetupHost != config.SetupDomain {
		t.Fatalf("SetupHost %q and config.SetupDomain %q must agree", SetupHost, config.SetupDomain)
	}
}

func TestPierLocalServesTheSetupPage(t *testing.T) {
	up := upstream(t)
	p := newProxy(t, up.URL)
	p.SetCA(testCA(t))
	get := func(handler http.Handler, url string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		return rec
	}

	for _, handler := range []http.Handler{p.HTTP(), p.HTTPS()} {
		if rec := get(handler, "http://pier.local/setup"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Pier Local CA") {
			t.Fatalf("pier.local/setup = %d, want the setup page", rec.Code)
		}
		if rec := get(handler, "http://Pier.local:8080/"); rec.Code != http.StatusFound || rec.Header().Get("Location") != SetupPath {
			t.Fatalf("pier.local/ = %d %q, want a redirect to /setup", rec.Code, rec.Header().Get("Location"))
		}
		if rec := get(handler, "http://pier.local"+CAPath); rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "-----BEGIN CERTIFICATE-----") {
			t.Fatalf("pier.local CA download = %d", rec.Code)
		}
		if rec := get(handler, "http://pier.local"+InstallPath); rec.Code != http.StatusOK {
			t.Fatalf("pier.local/.pier/ = %d, want the setup page there too", rec.Code)
		}
		if rec := get(handler, "http://pier.local/api"); rec.Code != http.StatusNotFound {
			t.Fatalf("pier.local/api = %d, want 404", rec.Code)
		}
	}

	// A project's own /setup is the project's.
	if rec := get(p.HTTPS(), "https://my-app.local/setup"); strings.Contains(rec.Body.String(), "Pier Local CA") {
		t.Fatal("my-app.local/setup served Pier's setup page instead of the service")
	}
}

func TestSetupURL(t *testing.T) {
	cases := []struct {
		http, https int
		want        string
	}{
		{80, 443, "http://pier.local/setup"},
		{8080, 443, "http://pier.local:8080/setup"},
		{0, 443, "https://pier.local/setup"},
		{0, 8443, "https://pier.local:8443/setup"},
	}
	for _, tc := range cases {
		if got := SetupURL(tc.http, tc.https); got != tc.want {
			t.Errorf("SetupURL(%d, %d) = %q, want %q", tc.http, tc.https, got, tc.want)
		}
	}
}
