package version

import "runtime/debug"

// Version is the server release displayed in the admin UI. Release builds can
// override it with: -ldflags "-X github.com/swilcox/kurokku-esp-server/internal/version.Version=v1.2.3"
var Version = "0.1.2"

func ServerVersion() string {
	if Version != "" && Version != "devel" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "0.1.1-dev"
}
