package cli

import "runtime/debug"

// version is the release this binary was built from. Release builds set it:
//
//	go build -ldflags "-X github.com/Bethel-nz/pier/internal/cli.version=v0.4.1"
var version = ""

// Version is this build's release, such as v0.4.1. A go install of a release
// reports its module version; a local build says dev.
func Version() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
