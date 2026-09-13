package app

import (
	"context"
	"errors"
	"testing"

	"pier/internal/reconcile"
)

func TestCopyReturnsURLWhenConfigured(t *testing.T) {
	env := newEnv()
	env.actual = env.desiredRoutes()
	var copied string
	svc := env.service()
	svc.copyURL = func(_ context.Context, url string) error {
		copied = url
		return nil
	}

	result, err := svc.Copy(context.Background(), CopyRequest{Start: env.project.Root, Service: "web"})
	if err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	if result.URL != "https://host.ts.net:8443/" || copied != result.URL {
		t.Fatalf("Copy() URL = %q copied = %q", result.URL, copied)
	}
}

func TestOpenRefusesUnconfiguredService(t *testing.T) {
	env := newEnv()
	env.actual = []reconcile.Route{}
	opened := false
	svc := env.service()
	svc.openURL = func(context.Context, string) error {
		opened = true
		return errors.New("should not open")
	}

	_, err := svc.Open(context.Background(), OpenRequest{Start: env.project.Root, Service: "web"})
	var notConfigured *NotConfiguredError
	if !errors.As(err, &notConfigured) {
		t.Fatalf("Open() error = %v, want *NotConfiguredError", err)
	}
	if opened {
		t.Fatal("Open() invoked the platform opener")
	}
}
