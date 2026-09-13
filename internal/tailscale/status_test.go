package tailscale

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseStatus(t *testing.T) {
	t.Run("normalizes HTTPS handlers and orders them by port then path", func(t *testing.T) {
		status, err := ParseStatus(readStatusFixture(t, "multiple-routes.json"))
		if err != nil {
			t.Fatalf("ParseStatus() error = %v", err)
		}

		want := Status{
			DNSName: "pier-test.example.ts.net",
			Routes: []Route{
				{HTTPSPort: 443, Path: "/", Target: "http://127.0.0.1:4100", Public: true},
				{HTTPSPort: 443, Path: "/api", Target: "http://127.0.0.1:4000", Public: true},
				{HTTPSPort: 8443, Path: "/", Target: "http://127.0.0.1:3000", Public: false},
				{HTTPSPort: 8443, Path: "/api", Target: "http://127.0.0.1:3100", Public: false},
			},
		}
		if !reflect.DeepEqual(status, want) {
			t.Errorf("ParseStatus() = %#v, want %#v", status, want)
		}
	})

	t.Run("returns no routes for an empty status", func(t *testing.T) {
		status, err := ParseStatus(readStatusFixture(t, "empty.json"))
		if err != nil {
			t.Fatalf("ParseStatus() error = %v", err)
		}
		if !reflect.DeepEqual(status, Status{}) {
			t.Errorf("ParseStatus() = %#v, want empty status", status)
		}
	})

	t.Run("classifies malformed JSON", func(t *testing.T) {
		_, err := ParseStatus(readStatusFixture(t, "malformed.json"))
		if !errors.Is(err, ErrInvalidStatus) {
			t.Errorf("ParseStatus() error = %v, want ErrInvalidStatus", err)
		}
	})
}

func readStatusFixture(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "tailscale", name))
	if err != nil {
		t.Fatalf("read status fixture %q: %v", name, err)
	}
	return data
}
