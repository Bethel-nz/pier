package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
}

func TestLogsCaptureStdoutStderrWithPrefixAndPersist(t *testing.T) {
	skipWindows(t)
	dir := t.TempDir()
	var out syncBuffer
	s := Start(context.Background(), []Process{
		{Name: "api", Command: "echo out; echo err >&2", Dir: t.TempDir(), Restart: RestartNever},
	}, &out, Options{LogDir: dir})
	s.Wait()
	for _, want := range []string{"api │ out\n", "api │ err\n"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("terminal missing %q:\n%s", want, out.String())
		}
	}
	entries, err := ReadLogs(dir, []string{"api"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, e := range entries {
		texts = append(texts, e.Text)
	}
	got := strings.Join(texts, "|")
	if !strings.Contains(got, "out") || !strings.Contains(got, "err") || !strings.Contains(got, "pier: exited") {
		t.Fatalf("persisted = %q", got)
	}
}

func TestLogsQuietKeepsTerminalClearButLogsEverything(t *testing.T) {
	skipWindows(t)
	dir := t.TempDir()
	var out syncBuffer
	s := Start(context.Background(), []Process{
		{Name: "web", Command: "echo chatter; exit 1", Dir: t.TempDir(), Restart: RestartNever},
	}, &out, Options{LogDir: dir, Quiet: true})
	s.Wait()
	if strings.Contains(out.String(), "chatter") {
		t.Fatalf("quiet printed process output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "web │ pier: exited with code 1") {
		t.Fatalf("quiet hid the crash:\n%s", out.String())
	}
	entries, _ := ReadLogs(dir, nil, 0)
	if len(entries) == 0 || entries[0].Text != "chatter" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestLogsConcurrentServicesKeepLinesWhole(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	p := newPrefixer(&out, []string{"a", "b"}, false)
	p.sink = newLogSink(dir)
	var wg sync.WaitGroup
	for _, name := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := p.writer(name)
			for i := range 200 {
				_, _ = fmt.Fprintf(w, "%s-line-%d with some padding\n", name, i)
			}
		}()
	}
	wg.Wait()
	p.sink.close()
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 400 {
		t.Fatalf("got %d lines", len(lines))
	}
	for _, line := range lines {
		name := line[:1]
		if !strings.HasPrefix(line, name+" │ "+name+"-line-") || !strings.HasSuffix(line, "with some padding") {
			t.Fatalf("corrupted line %q", line)
		}
	}
}

func TestLogsRotateAndBoundDisk(t *testing.T) {
	previous := maxLogBytes
	maxLogBytes = 1000
	t.Cleanup(func() { maxLogBytes = previous })
	dir := t.TempDir()
	sink := newLogSink(dir)
	for i := range 100 {
		sink.write("api", fmt.Sprintf("line %03d %s", i, strings.Repeat("x", 40)))
	}
	sink.close()
	for _, name := range []string{"api.log", "api.log.1"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Size() > maxLogBytes {
			t.Fatalf("%s: %v size %v", name, err, info)
		}
	}
	entries, _ := ReadLogs(dir, []string{"api"}, 0)
	if last := entries[len(entries)-1].Text; !strings.HasPrefix(last, "line 099") {
		t.Fatalf("newest line lost: %q", last)
	}
	if got, _ := ReadLogs(dir, []string{"api"}, 5); len(got) != 5 {
		t.Fatalf("tail returned %d", len(got))
	}
}

func TestLogsHugeLineIsBounded(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	p := newPrefixer(&out, []string{"api"}, false)
	p.sink = newLogSink(dir)
	w := p.writer("api")
	_, _ = w.Write([]byte(strings.Repeat("y", maxLineBytes*3)))
	if p.pending["api"].Len() > maxLineBytes {
		t.Fatal("pending buffer grew past the limit")
	}
	p.flush("api")
	p.sink.close()
	if out.Len() < maxLineBytes*3 {
		t.Fatal("bytes were dropped")
	}
}

func TestLogsSurviveRestartAndAppend(t *testing.T) {
	skipWindows(t)
	dir := t.TempDir()
	for i := range 2 {
		var out syncBuffer
		s := Start(context.Background(), []Process{
			{Name: "api", Command: fmt.Sprintf("echo run-%d", i), Dir: t.TempDir(), Restart: RestartNever},
		}, &out, Options{LogDir: dir, Quiet: true})
		s.Wait()
	}
	entries, _ := ReadLogs(dir, []string{"api"}, 0)
	var joined string
	for _, e := range entries {
		joined += e.Text + "\n"
	}
	if !strings.Contains(joined, "run-0") || !strings.Contains(joined, "run-1") {
		t.Fatalf("logs did not span supervisor runs:\n%s", joined)
	}
}

func TestFollowLogsSeesNewLines(t *testing.T) {
	dir := t.TempDir()
	sink := newLogSink(dir)
	sink.write("api", "old")
	ctx, cancel := context.WithCancel(context.Background())
	var out syncBuffer
	done := make(chan error, 1)
	go func() { done <- FollowLogs(ctx, &out, dir, nil, 10, false) }()
	waitFor(t, &out, "api │ old")
	sink.write("api", "fresh")
	waitFor(t, &out, "api │ fresh")
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("follow ignored cancellation")
	}
	sink.close()
}
