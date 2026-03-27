package cli

import (
	"runtime"
	"strings"
	"testing"

	"github.com/leonardkore/claude-statusline-patch/internal/patch"
)

func TestFormatCheckOutputIncludesKnownShapeAndSupportClaim(t *testing.T) {
	t.Parallel()

	out := formatCheckOutput("/tmp/claude", patch.Inspection{
		State:            patch.StateUnpatched,
		ShapeState:       patch.ShapeStateKnown,
		PatchState:       patch.PatchStateUnpatched,
		Version:          "2.1.85",
		ShapeID:          patch.ShapeIDStatuslineDebounceV1,
		ObservedVersions: []string{"2.1.84", "2.1.85"},
	}, false)

	expectedClaim := "patchable_only"
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		expectedClaim = "live_verified"
	}

	for _, fragment := range []string{
		"binary: /tmp/claude",
		"version: 2.1.85",
		"state: unpatched",
		"shape_id: statusline_debounce_v1",
		"observed_versions: 2.1.84, 2.1.85",
		"shape_state: known",
		"patch_state: unpatched",
		"support_claim: " + expectedClaim,
		"quick_apply_candidate: true",
		"managed: false",
	} {
		if !strings.Contains(out, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, out)
		}
	}
}

func TestFormatCheckOutputSuppressesKnownShapeFieldsForUnknownShape(t *testing.T) {
	t.Parallel()

	out := formatCheckOutput("/tmp/claude", patch.Inspection{
		State:      patch.StateUnrecognizedShape,
		ShapeState: patch.ShapeStateUnrecognized,
		PatchState: patch.PatchStateUnknown,
		Version:    "2.1.85",
	}, false)

	if strings.Contains(out, "shape_id:") {
		t.Fatalf("did not expect shape_id in output: %q", out)
	}
	if strings.Contains(out, "observed_versions:") {
		t.Fatalf("did not expect observed_versions in output: %q", out)
	}
	if !strings.Contains(out, "support_claim: undocumented") {
		t.Fatalf("expected undocumented claim, got %q", out)
	}
	if !strings.Contains(out, "quick_apply_candidate: false") {
		t.Fatalf("expected quick_apply_candidate false, got %q", out)
	}
}

func TestFormatDryRunOutputIncludesValidationMarkers(t *testing.T) {
	t.Parallel()

	out := formatDryRunOutput("/tmp/claude", patch.Inspection{
		State:            patch.StateUnpatched,
		ShapeState:       patch.ShapeStateKnown,
		PatchState:       patch.PatchStateUnpatched,
		Version:          "9.9.9",
		ShapeID:          patch.ShapeIDStatuslineDebounceV1,
		ObservedVersions: []string{"2.1.84", "2.1.85"},
	}, false, 1000)

	for _, fragment := range []string{
		"support_claim: patchable_only",
		"quick_apply_candidate: true",
		"dry_run: ok",
		"dry_run_rebuild_validation: passed",
		"would_apply_interval_ms: 1000",
	} {
		if !strings.Contains(out, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, out)
		}
	}
}
