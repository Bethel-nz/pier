package localname

import (
	"strings"
	"testing"

	"github.com/Bethel-nz/pier/internal/state"
)

func TestRecordsRejectsANameClaimedTwice(t *testing.T) {
	_, err := Records([]state.ProjectState{
		{ProjectID: "a", Domains: []state.LocalDomain{{Service: "api", Name: "my-app.local", Port: 4000}}},
		{ProjectID: "b", Domains: []state.LocalDomain{{Service: "api", Name: "my-app.local", Port: 4000}}},
	})
	if err == nil || !strings.Contains(err.Error(), "my-app.local") {
		t.Fatalf("Records() error = %v, want a conflict for my-app.local", err)
	}
}
