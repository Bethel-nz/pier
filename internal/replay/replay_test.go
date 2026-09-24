package replay

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Bethel-nz/pier/internal/capture"
)

func TestSendReplaysMethodPathBodyAndSafeHeaders(t *testing.T) {
	var got *http.Request
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Location", "/elsewhere")
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write([]byte("moved"))
	}))
	defer server.Close()

	original := capture.Exchange{
		ID: 42, Host: "myapp.local", Method: "POST", URL: "/hooks/stripe?id=7",
		RequestHeader: http.Header{
			"Authorization":     {"[redacted]"},
			"Stripe-Signature":  {"v1=abc"},
			"Transfer-Encoding": {"chunked"},
		},
		RequestBody: []byte(`{"paid":true}`), Status: 500, ResponseBody: []byte("boom"),
	}
	result := Send(context.Background(), Client(), server.URL, original)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if got.Method != "POST" || got.URL.RequestURI() != "/hooks/stripe?id=7" || body != `{"paid":true}` {
		t.Fatalf("service got %s %s %q", got.Method, got.URL.RequestURI(), body)
	}
	if got.Header.Get("Authorization") != "" || got.Header.Get("Stripe-Signature") != "v1=abc" ||
		got.Header.Get("X-Pier-Replay") != "42" || got.Header.Get("X-Forwarded-Host") != "myapp.local" {
		t.Fatalf("headers = %v", got.Header)
	}
	if result.Status != http.StatusFound || string(result.Body) != "moved" || !result.BodyChanged() {
		t.Fatalf("redirect was followed or misread: %+v", result)
	}
}
