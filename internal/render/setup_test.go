package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/localname"
)

func TestUpSendsPhonesToPierLocal(t *testing.T) {
	services := []app.ServiceInfo{{Name: "web", Domain: "myapp.local", LocalURL: "https://myapp.local/", LocalState: "live"}}
	up := func(report localname.Report) string {
		var out bytes.Buffer
		if err := (Options{Out: &out, Err: &out}).Up(app.UpResult{Services: services, Local: report}, nil); err != nil {
			t.Fatalf("Up() error = %v", err)
		}
		return out.String()
	}
	report := localname.Report{Running: true, CAPath: "/ca.pem", CATrusted: true, TrustedNow: true, SetupURL: "http://pier.local/setup"}
	if got := up(report); !strings.Contains(got, "phones       open http://pier.local/setup once") {
		t.Errorf("Up() = %q, want phones sent to pier.local", got)
	}
	report.SetupURL = "" // pier.local not live, such as when another machine holds it
	if got := up(report); !strings.Contains(got, "phones       open http://myapp.local/.pier/ once") {
		t.Errorf("Up() = %q, want the project's own setup page", got)
	}
}
