package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestVersionFlagPrintsTheRelease(t *testing.T) {
	previous := version
	version = "v1.2.3"
	t.Cleanup(func() { version = previous })

	stdout, _, err := runCLI(t, &fakeApp{}, "--version")
	if err != nil || stdout != "pier v1.2.3\n" {
		t.Fatalf("--version = %q, %v; want pier v1.2.3", stdout, err)
	}
}

func TestRefusedCommandLinesSayWhy(t *testing.T) {
	for _, args := range [][]string{{"tui"}, {"status", "--nope"}} {
		_, stderr, err := runCLI(t, &fakeApp{}, args...)
		if err == nil {
			t.Fatalf("%v: error = nil", args)
		}
		if !strings.HasPrefix(stderr, "Error: ") || !strings.Contains(stderr, "Run 'pier --help' for usage.") {
			t.Errorf("%v: stderr = %q, want the error and a pointer to help", args, stderr)
		}
	}
}

func TestCommandErrorsAreNotPrintedTwice(t *testing.T) {
	_, stderr, err := runCLI(t, &fakeApp{openErr: errors.New("no such service")}, "open", "web")
	if err == nil {
		t.Fatal("open error = nil")
	}
	if strings.Contains(stderr, "Run 'pier --help' for usage.") {
		t.Errorf("stderr = %q: a command's own error got the usage hint", stderr)
	}
}
