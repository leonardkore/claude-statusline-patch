package patch

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectKnownUnpatchedFixtures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		version     string
		fixtureName string
		verified    bool
	}{
		{name: "2.1.84", version: "2.1.84", fixtureName: "statusline-unpatched.js", verified: true},
		{name: "2.1.85", version: "2.1.85", fixtureName: "statusline-unpatched-2.1.85.js", verified: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			payload := append(versionBytes(tc.version), loadFixture(t, tc.fixtureName)...)

			inspection := Inspect(payload)
			if inspection.State != StateUnpatched {
				t.Fatalf("expected unpatched, got %s", inspection.State)
			}
			if inspection.ShapeState != ShapeStateKnown {
				t.Fatalf("expected known shape, got %s", inspection.ShapeState)
			}
			if inspection.PatchState != PatchStateUnpatched {
				t.Fatalf("expected patch_state unpatched, got %s", inspection.PatchState)
			}
			if inspection.ShapeID != ShapeIDStatuslineDebounceV1 {
				t.Fatalf("expected shape id %s, got %s", ShapeIDStatuslineDebounceV1, inspection.ShapeID)
			}
			if IsDocumentedLiveVerifiedVersion(tc.version) != tc.verified {
				t.Fatalf("expected documented live verified %t for %s", tc.verified, tc.version)
			}
		})
	}
}

func TestApplyIsIdempotentForSameInterval(t *testing.T) {
	t.Parallel()

	payload := append(versionBytes("2.1.84"), loadFixture(t, "statusline-unpatched.js")...)

	patched, err := Apply(payload, 1000)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	inspection := Inspect(patched)
	if inspection.State != StatePatched || inspection.IntervalMS != 1000 {
		t.Fatalf("unexpected inspection after patch: %+v", inspection)
	}

	if _, err := Apply(patched, 1000); err == nil {
		t.Fatalf("expected re-apply to fail because payload is already patched")
	}
}

func TestInspectFailsCleanlyOnUnknownShape(t *testing.T) {
	t.Parallel()

	payload := append(versionBytes("2.1.85"), []byte("console.log('unknown shape');")...)

	inspection := Inspect(payload)
	if inspection.State != StateUnrecognizedShape {
		t.Fatalf("expected unrecognized shape, got %s", inspection.State)
	}
	if inspection.ShapeState != ShapeStateUnrecognized {
		t.Fatalf("expected unrecognized shape_state, got %s", inspection.ShapeState)
	}
}

func TestKnownShapeStillPatchesForUnverifiedVersion(t *testing.T) {
	t.Parallel()

	payload := append(versionBytes("9.9.9"), loadFixture(t, "statusline-unpatched-2.1.85.js")...)

	inspection := Inspect(payload)
	if inspection.State != StateUnpatched {
		t.Fatalf("expected unpatched, got %s", inspection.State)
	}
	if inspection.ShapeID != ShapeIDStatuslineDebounceV1 {
		t.Fatalf("expected known shape id, got %s", inspection.ShapeID)
	}
	if IsDocumentedLiveVerifiedVersion(inspection.Version) {
		t.Fatalf("did not expect synthetic version to be documented live-verified")
	}
	if _, err := ApplyInspection(payload, inspection, 1000); err != nil {
		t.Fatalf("expected quick-apply known shape to patch, got %v", err)
	}
}

func TestInspectPatchedVsUnpatchedFixtures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name             string
		version          string
		unpatchedFixture string
		patchedFixture   string
	}{
		{name: "2.1.84", version: "2.1.84", unpatchedFixture: "statusline-unpatched.js", patchedFixture: "statusline-patched-1000.js"},
		{name: "2.1.85", version: "2.1.85", unpatchedFixture: "statusline-unpatched-2.1.85.js", patchedFixture: "statusline-patched-1000-2.1.85.js"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			unpatched := append(versionBytes(tc.version), loadFixture(t, tc.unpatchedFixture)...)
			patched := append(versionBytes(tc.version), loadFixture(t, tc.patchedFixture)...)

			unpatchedInspection := Inspect(unpatched)
			patchedInspection := Inspect(patched)

			if unpatchedInspection.State != StateUnpatched {
				t.Fatalf("expected unpatched, got %s", unpatchedInspection.State)
			}
			if patchedInspection.State != StatePatched || patchedInspection.IntervalMS != 1000 {
				t.Fatalf("unexpected patched inspection: %+v", patchedInspection)
			}
			if patchedInspection.ShapeID != ShapeIDStatuslineDebounceV1 {
				t.Fatalf("expected shape id %s, got %s", ShapeIDStatuslineDebounceV1, patchedInspection.ShapeID)
			}
		})
	}
}

func TestApplyProducesExpectedPatchedFixture(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name             string
		version          string
		unpatchedFixture string
		patchedFixture   string
	}{
		{name: "2.1.84", version: "2.1.84", unpatchedFixture: "statusline-unpatched.js", patchedFixture: "statusline-patched-1000.js"},
		{name: "2.1.85", version: "2.1.85", unpatchedFixture: "statusline-unpatched-2.1.85.js", patchedFixture: "statusline-patched-1000-2.1.85.js"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			payload := append(versionBytes(tc.version), loadFixture(t, tc.unpatchedFixture)...)
			expected := append(versionBytes(tc.version), loadFixture(t, tc.patchedFixture)...)

			patched, err := Apply(payload, 1000)
			if err != nil {
				t.Fatalf("apply failed: %v", err)
			}
			if !bytes.Equal(patched, expected) {
				t.Fatalf("patched payload does not match expected fixture")
			}
		})
	}
}

func TestInspectMalformedPatchedIntervalIsAmbiguous(t *testing.T) {
	t.Parallel()

	payload := append(versionBytes("2.1.85"), []byte(`,unused1=tX.useEffect(()=>{const id=setInterval(()=>L(),99999999999999999999999999999999);return()=>clearInterval(id);},[L]),Z=tX.useCallback(()=>{},[]);tX.useEffect(()=>{if($!==X.current.messageId||_!==X.current.permissionMode||q!==X.current.vimMode)X.current.permissionMode=_,X.current.vimMode=q,Z()},[$,_,q,Z]);`)...)

	inspection := Inspect(payload)
	if inspection.State != StateAmbiguousShape {
		t.Fatalf("expected ambiguous shape, got %s", inspection.State)
	}
}

func TestInspectDuplicateMatchesAreAmbiguous(t *testing.T) {
	t.Parallel()

	payload := append(versionBytes("2.1.85"), append(loadFixture(t, "statusline-unpatched-2.1.85.js"), loadFixture(t, "statusline-unpatched-2.1.85.js")...)...)

	inspection := Inspect(payload)
	if inspection.State != StateAmbiguousShape {
		t.Fatalf("expected ambiguous shape, got %s", inspection.State)
	}
	if inspection.ShapeState != ShapeStateAmbiguous {
		t.Fatalf("expected ambiguous shape_state, got %s", inspection.ShapeState)
	}
}

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func versionBytes(version string) []byte {
	return []byte(`VERSION:"` + version + `";`)
}
