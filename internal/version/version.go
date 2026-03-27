package version

import "runtime/debug"

var Version = "dev"

var readBuildInfo = debug.ReadBuildInfo

func String() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	info, ok := readBuildInfo()
	if !ok || info == nil {
		return Version
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return Version
}
