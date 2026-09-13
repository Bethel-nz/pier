package platform

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestOpenSelectsOSCommand(t *testing.T) {
	tests := []struct {
		goos string
		name string
		args []string
	}{
		{"darwin", "open", []string{"https://host.ts.net/"}},
		{"linux", "xdg-open", []string{"https://host.ts.net/"}},
		{"windows", "rundll32", []string{"url.dll,FileProtocolHandler", "https://host.ts.net/"}},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			var gotName string
			var gotArgs []string
			actions := Actions{
				GOOS: tt.goos,
				Run: func(_ context.Context, stdin, name string, args ...string) error {
					gotName, gotArgs = name, append([]string(nil), args...)
					if stdin != "" {
						t.Errorf("Open stdin = %q, want empty", stdin)
					}
					return nil
				},
			}
			if err := actions.Open(context.Background(), "https://host.ts.net/"); err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			if gotName != tt.name || !reflect.DeepEqual(gotArgs, tt.args) {
				t.Fatalf("Open() = %s %q, want %s %q", gotName, gotArgs, tt.name, tt.args)
			}
		})
	}
}

func TestCopySelectsOSCommand(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		lookPath LookPath
		want     []string
	}{
		{"darwin", "darwin", nil, []string{"pbcopy"}},
		{"windows", "windows", nil, []string{"clip"}},
		{"linux wl-copy", "linux", func(file string) (string, error) {
			if file == "wl-copy" {
				return "/usr/bin/wl-copy", nil
			}
			return "", errors.New("missing")
		}, []string{"wl-copy"}},
		{"linux xclip fallback", "linux", func(file string) (string, error) {
			if file == "xclip" {
				return "/usr/bin/xclip", nil
			}
			return "", errors.New("missing")
		}, []string{"xclip", "-selection", "clipboard"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			var gotStdin string
			actions := Actions{
				GOOS:     tt.goos,
				LookPath: tt.lookPath,
				Run: func(_ context.Context, stdin, name string, args ...string) error {
					gotStdin = stdin
					got = append([]string{name}, args...)
					return nil
				},
			}
			if err := actions.Copy(context.Background(), "https://host.ts.net/"); err != nil {
				t.Fatalf("Copy() error = %v", err)
			}
			if gotStdin != "https://host.ts.net/" {
				t.Errorf("Copy stdin = %q", gotStdin)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Copy() command = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCopyLinuxFallsBackWhenWlCopyFails(t *testing.T) {
	var ran []string
	actions := Actions{
		GOOS: "linux",
		LookPath: func(file string) (string, error) {
			return "/usr/bin/" + file, nil
		},
		Run: func(_ context.Context, _ string, name string, args ...string) error {
			ran = append(ran, name)
			if name == "wl-copy" {
				return errors.New("wayland unavailable")
			}
			return nil
		},
	}
	if err := actions.Copy(context.Background(), "value"); err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	if !reflect.DeepEqual(ran, []string{"wl-copy", "xclip"}) {
		t.Fatalf("Copy() ran %q, want wl-copy then xclip", ran)
	}
}
