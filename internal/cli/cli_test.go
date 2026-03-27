package cli

import (
	"runtime"
	"strings"
	"testing"

	"github.com/leonardkore/claude-statusline-patch/internal/patch"
)

func TestFormatCheckOutputIncludesKnownShapeAndVerifiedClaim(t *testing.T) {
	t.Parallel()

	out := formatCheckOutput("/tmp/claude", patch.Inspection{
		State:      patch.StateUnpatched,
		ShapeState: patch.ShapeStateKnown,
		PatchState: patch.PatchStateUnpatched,
		Version:    "2.1.85",
		ShapeID:    patch.ShapeIDStatuslineDebounceV1,
	}, false)

	expectedClaim := "not-live-verified"
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		expectedClaim = "live-verified"
	}

	for _, fragment := range []string{
		"binary: /tmp/claude",
		"version: 2.1.85",
		"state: unpatched",
		"shape_id: statusline_debounce_v1",
		"shape_state: known",
		"patch_state: unpatched",
		"verification_claim: " + expectedClaim,
		"managed: false",
	} {
		if !strings.Contains(out, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, out)
		}
	}
}

func TestFormatCheckOutputSuppressesVerifiedClaimForUnknownShape(t *testing.T) {
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
	if !strings.Contains(out, "verification_claim: not-live-verified") {
		t.Fatalf("expected not-live-verified claim, got %q", out)
	}
}
