package patch

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectUnpatchedFixture(t *testing.T) {
	payload := append(versionBytes(SupportedVersion), loadFixture(t, "statusline-unpatched.js")...)

	inspection := Inspect(payload)
	if inspection.State != StateUnpatched {
		t.Fatalf("expected unpatched, got %s", inspection.State)
	}
	if inspection.Version != SupportedVersion {
		t.Fatalf("expected version %s, got %s", SupportedVersion, inspection.Version)
	}
}

func TestApplyIsIdempotentForSameInterval(t *testing.T) {
	payload := append(versionBytes(SupportedVersion), loadFixture(t, "statusline-unpatched.js")...)

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
	payload := append(versionBytes(SupportedVersion), []byte("console.log('unknown shape');")...)

	inspection := Inspect(payload)
	if inspection.State != StateAmbiguous {
		t.Fatalf("expected ambiguous, got %s", inspection.State)
	}
}

func TestInspectPatchedVsUnpatchedFixtures(t *testing.T) {
	unpatched := append(versionBytes(SupportedVersion), loadFixture(t, "statusline-unpatched.js")...)
	patched := append(versionBytes(SupportedVersion), loadFixture(t, "statusline-patched-1000.js")...)

	unpatchedInspection := Inspect(unpatched)
	patchedInspection := Inspect(patched)

	if unpatchedInspection.State != StateUnpatched {
		t.Fatalf("expected unpatched, got %s", unpatchedInspection.State)
	}
	if patchedInspection.State != StatePatched || patchedInspection.IntervalMS != 1000 {
		t.Fatalf("unexpected patched inspection: %+v", patchedInspection)
	}
}

func TestApplyProducesExpectedPatchedFixture(t *testing.T) {
	payload := append(versionBytes(SupportedVersion), loadFixture(t, "statusline-unpatched.js")...)
	expected := append(versionBytes(SupportedVersion), loadFixture(t, "statusline-patched-1000.js")...)

	patched, err := Apply(payload, 1000)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if !bytes.Equal(patched, expected) {
		t.Fatalf("patched payload does not match expected fixture")
	}
}

func TestInspectMalformedPatchedIntervalIsAmbiguous(t *testing.T) {
	payload := append(versionBytes(SupportedVersion), append(append([]byte(nil), patchedPrefix...), append(bytes.Repeat([]byte("9"), 32), patchedSuffix...)...)...)

	inspection := Inspect(payload)
	if inspection.State != StateAmbiguous {
		t.Fatalf("expected ambiguous, got %s", inspection.State)
	}
}

func TestInspectDuplicateMatchesAreAmbiguous(t *testing.T) {
	payload := append(versionBytes(SupportedVersion), append(loadFixture(t, "statusline-unpatched.js"), loadFixture(t, "statusline-unpatched.js")...)...)

	inspection := Inspect(payload)
	if inspection.State != StateAmbiguous {
		t.Fatalf("expected ambiguous, got %s", inspection.State)
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
