package app

import "runtime/debug"

// Version is the release version, stamped by `just release` with
// -ldflags "-X github.com/wasichu/go-whassup/internal/app.Version=v1.2.3".
var Version = "dev"

// version is Version, or for a build without the stamp, the module
// version `go install …@v1.2.3` records (else "dev").
func version() string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return Version
}
