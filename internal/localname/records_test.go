package localname

import (
	"strings"
	"testing"

	"pier/internal/state"
)

func TestRecordsRejectsANameClaimedTwice(t *testing.T) {
	_, err := Records([]state.ProjectState{
		{ProjectID: "a", Domains: []state.LocalDomain{{Service: "api", Name: "holo-api.local", Port: 4000}}},
		{ProjectID: "b", Domains: []state.LocalDomain{{Service: "api", Name: "holo-api.local", Port: 4000}}},
	})
	if err == nil || !strings.Contains(err.Error(), "holo-api.local") {
		t.Fatalf("Records() error = %v, want a conflict for holo-api.local", err)
	}
}
