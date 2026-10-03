package config

import (
	"slices"
	"testing"
)

func TestValidateHealthAndRestart(t *testing.T) {
	project := loadAndNormalize(t, `version: 1
name: demo
services:
  api:
    target: localhost:4000
    run: bun dev
    restart: never
    health: /healthz
  web:
    target: localhost:3000
    path: /web
    run: bun dev
    restart: always
    health: healthz
  db:
    target: localhost:5432
    protocol: tcp
    health: /ping
  static:
    target: localhost:8080
    path: /static
    restart: never
`)
	if api := project.Services[0]; api.Name != "api" || api.Run.Restart != RestartNever || api.Health != "/healthz" {
		t.Fatalf("api = %+v", api)
	}
	got := validationFields(Validate(project))
	want := []string{"db.health", "static.restart", "web.health", "web.restart"}
	if !slices.Equal(got, want) {
		t.Fatalf("Validate() fields = %v, want %v", got, want)
	}
}
