package localname

import (
	"strings"
	"testing"

	"github.com/Bethel-nz/pier/internal/state"
)

func TestAutostartFollowsProjectsThatAskForIt(t *testing.T) {
	withNames := []state.LocalDomain{{Name: "myapp.local", Target: "http://127.0.0.1:3000"}}
	cases := []struct {
		name     string
		projects []state.ProjectState
		want     bool
	}{
		{"nobody asks", []state.ProjectState{{Domains: withNames}}, false},
		{"one project asks", []state.ProjectState{{Domains: withNames}, {Domains: withNames, Local: state.LocalSettings{Autostart: true}}}, true},
		{"asks but serves nothing", []state.ProjectState{{Local: state.LocalSettings{Autostart: true}}}, false},
	}
	for _, tc := range cases {
		if got := wantsAutostart(tc.projects); got != tc.want {
			t.Errorf("%s: wantsAutostart = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLoginItemsRunLocaldAndSurviveOnlyCrashes(t *testing.T) {
	plist := launchdPlist("/Users/ren/go/bin/pier", "/Users/ren/Library/Application Support/pier/pierd.log")
	for _, want := range []string{"<string>dev.pier.locald</string>", "<string>/Users/ren/go/bin/pier</string>", "<string>locald</string>", "<key>SuccessfulExit</key>"} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist missing %s", want)
		}
	}
	unit := systemdUnit("/home/ren/go/bin/pier")
	for _, want := range []string{`ExecStart="/home/ren/go/bin/pier" locald`, "Restart=on-failure", "WantedBy=default.target"} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %s", want)
		}
	}
}
