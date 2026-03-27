package version

import (
	"runtime/debug"
	"testing"
)

func TestStringPrefersInjectedVersion(t *testing.T) {
	originalVersion := Version
	originalReadBuildInfo := readBuildInfo
	t.Cleanup(func() {
		Version = originalVersion
		readBuildInfo = originalReadBuildInfo
	})

	Version = "v0.1.3"
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}}, true
	}

	if got := String(); got != "v0.1.3" {
		t.Fatalf("expected injected version, got %q", got)
	}
}

func TestStringFallsBackToModuleVersion(t *testing.T) {
	originalVersion := Version
	originalReadBuildInfo := readBuildInfo
	t.Cleanup(func() {
		Version = originalVersion
		readBuildInfo = originalReadBuildInfo
	})

	Version = "dev"
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v0.1.2"}}, true
	}

	if got := String(); got != "v0.1.2" {
		t.Fatalf("expected module version fallback, got %q", got)
	}
}

func TestStringReturnsDevWithoutTaggedBuildInfo(t *testing.T) {
	originalVersion := Version
	originalReadBuildInfo := readBuildInfo
	t.Cleanup(func() {
		Version = originalVersion
		readBuildInfo = originalReadBuildInfo
	})

	Version = "dev"
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true
	}

	if got := String(); got != "dev" {
		t.Fatalf("expected dev fallback, got %q", got)
	}
}
