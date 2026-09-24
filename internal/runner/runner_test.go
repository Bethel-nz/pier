package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"**/*.go", "main.go", true},
		{"**/*.go", "internal/app/run.go", true},
		{"*.go", "internal/app/run.go", false},
		{"src/**", "src/a/b.ts", true},
		{"src/**/*.ts", "src/index.ts", true},
		{"src/**/*.ts", "lib/index.ts", false},
		{"templates/*.html", "templates/home.html", true},
		{"templates/*.html", "templates/x/home.html", false},
	}
	for _, c := range cases {
		if got := matchGlob(c.pattern, c.name); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestPrefixerKeepsLinesWhole(t *testing.T) {
	var out bytes.Buffer
	p := newPrefixer(&out, []string{"web", "api"}, false)
	web, api := p.writer("web"), p.writer("api")
	_, _ = web.Write([]byte("hel"))
	_, _ = api.Write([]byte("ready\n"))
	_, _ = web.Write([]byte("lo\npartial"))
	p.flush("web")
	want := "api │ ready\nweb │ hello\nweb │ partial\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

// syncBuffer is a bytes.Buffer safe for the supervisor's goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitFor(t *testing.T, out *syncBuffer, text string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), text) {
		if time.Now().After(deadline) {
			t.Fatalf("never saw %q in:\n%s", text, out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSupervisorStopsProcessTreeOnCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	s := Start(ctx, []Process{{Name: "web", Command: `echo "port $PORT"; sleep 30`, Dir: t.TempDir(), Env: map[string]string{"PORT": "3000"}}}, &out, false)
	waitFor(t, &out, "web │ port 3000")
	if got := s.Running(); len(got) != 1 {
		t.Fatalf("running = %v", got)
	}
	cancel()
	done := make(chan struct{})
	go func() { s.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("sleep kept running after cancel: the process group was not stopped")
	}
	waitFor(t, &out, "pier: stopped")
}

func TestSupervisorReportsExitAndDoesNotRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	var out syncBuffer
	s := Start(context.Background(), []Process{{Name: "api", Command: "exit 3", Dir: t.TempDir()}}, &out, false)
	s.Wait()
	if !strings.Contains(out.String(), "api │ pier: exited with code 3\n") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestSupervisorRestartsOnWatchedChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	previous := pollEvery
	pollEvery = 50 * time.Millisecond
	t.Cleanup(func() { pollEvery = previous })

	dir := t.TempDir()
	source := filepath.Join(dir, "cmd", "main.go")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := Start(ctx, []Process{{Name: "api", Command: "echo started; sleep 30", Dir: dir, Watch: []string{"**/*.go"}}}, &out, false)
	waitFor(t, &out, "api │ started")

	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(out.String(), "restarting") {
		t.Fatal("a file outside the globs restarted the process")
	}

	if err := os.WriteFile(source, []byte("v2 is longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, &out, "pier: restarting: cmd/main.go changed")
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(out.String(), "api │ started") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("did not start again:\n%s", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	s.Wait()
}
