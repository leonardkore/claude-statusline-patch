package patch

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
				if fixture.ExtractedAt == "" {
					t.Fatalf("expected authoritative fixture to record extraction date")
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

	payload := fixturePayloadByID(t, "claude-2.1.85-unpatched")
	inspection := Inspect(payload)
	if inspection.ShapeID != ShapeIDStatuslineDebounceV1 {
		t.Fatalf("expected shape id %s, got %s", ShapeIDStatuslineDebounceV1, inspection.ShapeID)
	}
	expected := []string{"2.1.84", "2.1.85"}
	if strings.Join(inspection.ObservedVersions, ",") != strings.Join(expected, ",") {
		t.Fatalf("expected observed versions %v, got %v", expected, inspection.ObservedVersions)
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

func TestExtractMatchedSnippetReturnsKnownSnippet(t *testing.T) {
	t.Parallel()

	payload := fixturePayloadByID(t, "claude-2.1.85-unpatched")
	snippet, inspection, err := ExtractMatchedSnippet(payload)
	if err != nil {
		t.Fatalf("extract matched snippet failed: %v", err)
	}
	expected := bytes.TrimSuffix(loadFixture(t, "claude-2.1.85-unpatched.js"), []byte("\n"))
	if inspection.ShapeID != ShapeIDStatuslineDebounceV1 {
		t.Fatalf("expected shape id %s, got %s", ShapeIDStatuslineDebounceV1, inspection.ShapeID)
	}
	if !bytes.Equal(snippet, expected) {
		t.Fatalf("expected extracted snippet to match fixture bytes")
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
