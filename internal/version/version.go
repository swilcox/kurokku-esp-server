package version

import "runtime/debug"

// Version is the server release displayed in the admin UI. Release builds
// inject the git tag via: -ldflags "-X github.com/swilcox/kurokku-esp-server/internal/version.Version=v1.2.3"
var Version = "dev"

func ServerVersion() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
