package patch

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fixtureManifest struct {
	Fixtures []fixtureRecord `json:"fixtures"`
}

type fixtureRecord struct {
	ID              string `json:"id"`
	Path            string `json:"path"`
	Version         string `json:"version"`
	PatchState      string `json:"patch_state"`
	State           string `json:"state"`
	ShapeID         string `json:"shape_id"`
	IntervalMS      int    `json:"interval_ms"`
	Authoritative   bool   `json:"authoritative"`
	SupportClaim    string `json:"support_claim"`
	SourceBinarySHA string `json:"source_binary_sha256"`
	GeneratedByTool string `json:"generated_by_tool_version"`
	ExtractedAt     string `json:"extracted_at"`
	Notes           string `json:"notes"`
}

func TestManifestFixturesInspectAsDeclared(t *testing.T) {
	t.Parallel()

	for _, fixture := range loadManifest(t).Fixtures {
		fixture := fixture
		t.Run(fixture.ID, func(t *testing.T) {
			payload := fixturePayload(t, fixture)
			inspection := Inspect(payload)

			if fixture.Authoritative {
				if fixture.SourceBinarySHA == "" {
					t.Fatalf("expected authoritative fixture to record source binary sha")
				}
				if len(fixture.SourceBinarySHA) != 64 {
					t.Fatalf("expected authoritative fixture source binary sha to be 64 hex chars, got %q", fixture.SourceBinarySHA)
				}
				if _, err := hex.DecodeString(fixture.SourceBinarySHA); err != nil {
					t.Fatalf("expected authoritative fixture source binary sha to be valid hex: %v", err)
				}
				if fixture.ExtractedAt == "" {
					t.Fatalf("expected authoritative fixture to record extraction date")
				}
				if _, err := time.Parse("2006-01-02", fixture.ExtractedAt); err != nil {
					t.Fatalf("expected authoritative fixture extraction date to parse: %v", err)
				}
			}

			if string(inspection.State) != fixture.State {
				t.Fatalf("expected state %s, got %s", fixture.State, inspection.State)
			}
			if string(inspection.PatchState) != fixture.PatchState {
				t.Fatalf("expected patch_state %s, got %s", fixture.PatchState, inspection.PatchState)
			}
			if inspection.IntervalMS != fixture.IntervalMS {
				t.Fatalf("expected interval %d, got %d", fixture.IntervalMS, inspection.IntervalMS)
			}
			if fixture.ShapeID == "" {
				if inspection.ShapeID != "" {
					t.Fatalf("expected no shape_id, got %s", inspection.ShapeID)
				}
				return
			}
			if inspection.ShapeID != fixture.ShapeID {
				t.Fatalf("expected shape_id %s, got %s", fixture.ShapeID, inspection.ShapeID)
			}
			if len(inspection.ObservedVersions) == 0 {
				t.Fatalf("expected observed versions for known shape")
			}
		})
	}
}

func TestObservedVersionsMatchKnownAuthoritativeFixtures(t *testing.T) {
	t.Parallel()

	manifest := loadManifest(t)
	expectedByShape := map[string][]string{}
	for _, fixture := range manifest.Fixtures {
		if !fixture.Authoritative || fixture.ShapeID == "" {
			continue
		}
		expectedByShape[fixture.ShapeID] = append(expectedByShape[fixture.ShapeID], fixture.Version)
	}
	for shapeID, expected := range expectedByShape {
		got := ObservedVersions(shapeID)
		if len(got) != len(expected) {
			t.Fatalf("expected observed versions %v for %s, got %v", expected, shapeID, got)
		}
		for i := range expected {
			if got[i] != expected[i] {
				t.Fatalf("expected observed versions %v for %s, got %v", expected, shapeID, got)
			}
		}
	}
}

func TestApplyProducesManifestPatchedFixtures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		unpatchedID string
		patchedID   string
	}{
		{unpatchedID: "claude-2.1.84-unpatched", patchedID: "claude-2.1.84-patched-1000"},
		{unpatchedID: "claude-2.1.85-unpatched", patchedID: "claude-2.1.85-patched-1000"},
		{unpatchedID: "claude-2.1.86-unpatched", patchedID: "claude-2.1.86-patched-1000"},
		{unpatchedID: "claude-2.1.87-unpatched", patchedID: "claude-2.1.87-patched-1000"},
		{unpatchedID: "claude-2.1.89-unpatched", patchedID: "claude-2.1.89-patched-1000"},
		{unpatchedID: "claude-2.1.90-unpatched", patchedID: "claude-2.1.90-patched-1000"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.unpatchedID, func(t *testing.T) {
			unpatched := fixturePayloadByID(t, tc.unpatchedID)
			expected := fixturePayloadByID(t, tc.patchedID)

			patched, err := Apply(unpatched, 1000)
			if err != nil {
				t.Fatalf("apply failed: %v", err)
			}
			if !bytes.Equal(patched, expected) {
				t.Fatalf("patched payload does not match expected fixture")
			}
		})
	}
}

func TestApplyIsIdempotentForSameInterval(t *testing.T) {
	t.Parallel()

	payload := fixturePayloadByID(t, "claude-2.1.84-unpatched")

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

func TestKnownShapeStillPatchesForUnverifiedVersion(t *testing.T) {
	t.Parallel()

	payload := append(versionBytes("9.9.9"), loadFixture(t, "claude-2.1.85-unpatched.js")...)

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

func TestKnownShapeV2StillPatchesForUnverifiedVersion(t *testing.T) {
	t.Parallel()

	payload := append(versionBytes("9.9.9"), loadFixture(t, "claude-2.1.86-unpatched.js")...)

	inspection := Inspect(payload)
	if inspection.State != StateUnpatched {
		t.Fatalf("expected unpatched, got %s", inspection.State)
	}
	if inspection.ShapeID != ShapeIDStatuslineDebounceV2 {
		t.Fatalf("expected known shape id, got %s", inspection.ShapeID)
	}
	if IsDocumentedLiveVerifiedVersion(inspection.Version) {
		t.Fatalf("did not expect synthetic version to be documented live-verified")
	}
	if _, err := ApplyInspection(payload, inspection, 1000); err != nil {
		t.Fatalf("expected quick-apply known shape to patch, got %v", err)
	}
}

func TestExtractMatchedSnippetReturnsKnownSnippet(t *testing.T) {
	t.Parallel()

	payload := fixturePayloadByID(t, "claude-2.1.85-unpatched")
	snippet, inspection, err := ExtractMatchedSnippet(payload)
	if err != nil {
		t.Fatalf("extract matched snippet failed: %v", err)
	}
	expected := trimTrailingLineEndings(loadFixture(t, "claude-2.1.85-unpatched.js"))
	if inspection.ShapeID != ShapeIDStatuslineDebounceV1 {
		t.Fatalf("expected shape id %s, got %s", ShapeIDStatuslineDebounceV1, inspection.ShapeID)
	}
	if !bytes.Equal(snippet, expected) {
		t.Fatalf("expected extracted snippet to match fixture bytes")
	}
}

func TestExtractMatchedSnippetReturnsKnownSnippetV2(t *testing.T) {
	t.Parallel()

	payload := fixturePayloadByID(t, "claude-2.1.86-unpatched")
	snippet, inspection, err := ExtractMatchedSnippet(payload)
	if err != nil {
		t.Fatalf("extract matched snippet failed: %v", err)
	}
	expected := trimTrailingLineEndings(loadFixture(t, "claude-2.1.86-unpatched.js"))
	if inspection.ShapeID != ShapeIDStatuslineDebounceV2 {
		t.Fatalf("expected shape id %s, got %s", ShapeIDStatuslineDebounceV2, inspection.ShapeID)
	}
	if !bytes.Equal(snippet, expected) {
		t.Fatalf("expected extracted snippet to match fixture bytes")
	}
}

func TestInspectNegativeFixturesExposeExpectedShapeState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		id         string
		state      State
		shapeState ShapeState
	}{
		{id: "negative-2.1.85-duplicate-unpatched", state: StateAmbiguousShape, shapeState: ShapeStateAmbiguous},
		{id: "negative-2.1.85-malformed-patched-interval", state: StateAmbiguousShape, shapeState: ShapeStateAmbiguous},
		{id: "negative-2.1.85-unrecognized-delay", state: StateUnrecognizedShape, shapeState: ShapeStateUnrecognized},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.id, func(t *testing.T) {
			inspection := Inspect(fixturePayloadByID(t, tc.id))
			if inspection.State != tc.state {
				t.Fatalf("expected state %s, got %s", tc.state, inspection.State)
			}
			if inspection.ShapeState != tc.shapeState {
				t.Fatalf("expected shape state %s, got %s", tc.shapeState, inspection.ShapeState)
			}
		})
	}
}

func TestInspectForeignContentIsUnrecognized(t *testing.T) {
	t.Parallel()

	inspection := Inspect([]byte(`VERSION:"2.1.85";console.log("foreign content");`))
	if inspection.State != StateUnrecognizedShape {
		t.Fatalf("expected unrecognized shape, got %s", inspection.State)
	}
	if inspection.ShapeState != ShapeStateUnrecognized {
		t.Fatalf("expected unrecognized shape state, got %s", inspection.ShapeState)
	}
	if inspection.PatchState != PatchStateUnknown {
		t.Fatalf("expected unknown patch state, got %s", inspection.PatchState)
	}
}

func TestDetectVersionPrefersClaudeMetadataVersion(t *testing.T) {
	t.Parallel()

	payload := []byte(`VERSION:"1.0.0";console.log("foreign content");IMDS_VERSION:"2020-06-01",PACKAGE_URL:"@anthropic-ai/claude-code",README_URL:"https://code.claude.com/docs/en/overview",VERSION:"2.1.86",FEEDBACK_CHANNEL:"https://github.com/anthropics/claude-code/issues",BUILD_TIME:"2026-03-27T20:29:28Z"`)
	if got := DetectVersion(payload); got != "2.1.86" {
		t.Fatalf("expected 2.1.86, got %q", got)
	}
}

func loadManifest(t *testing.T) fixtureManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "statusline-fixtures.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest fixtureManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(manifest.Fixtures) == 0 {
		t.Fatalf("expected manifest fixtures")
	}
	return manifest
}

func fixturePayloadByID(t *testing.T, id string) []byte {
	t.Helper()
	for _, fixture := range loadManifest(t).Fixtures {
		if fixture.ID == id {
			return fixturePayload(t, fixture)
		}
	}
	t.Fatalf("fixture %s not found", id)
	return nil
}

func fixturePayload(t *testing.T, fixture fixtureRecord) []byte {
	t.Helper()
	data := loadFixture(t, fixture.Path)
	if fixture.Version == "" {
		return data
	}
	return append(versionBytes(fixture.Version), data...)
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

func trimTrailingLineEndings(data []byte) []byte {
	return bytes.TrimRight(data, "\r\n")
}
