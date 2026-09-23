package main

import (
	"context"
	"errors"
	"os"

	"github.com/Bethel-nz/pier/internal/app"
	"github.com/Bethel-nz/pier/internal/cli"
)

func main() {
	err := cli.Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		os.Exit(exitCode(err))
	}
}

func exitCode(err error) int {
	var invalid *app.InvalidConfigError
	if errors.As(err, &invalid) {
		return 2
	}
	var prerequisite *app.PrerequisiteError
	if errors.As(err, &prerequisite) {
		return 3
	}
	var conflict *app.ConflictError
	if errors.As(err, &conflict) {
		return 4
	}
	var apply *app.ApplyError
	if errors.As(err, &apply) {
		return 5
	}
	return 1
}
