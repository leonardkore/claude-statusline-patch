package cli

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/leonardkore/claude-statusline-patch/internal/backup"
	"github.com/leonardkore/claude-statusline-patch/internal/bun"
	"github.com/leonardkore/claude-statusline-patch/internal/patch"
	"github.com/leonardkore/claude-statusline-patch/internal/repack"
	"github.com/leonardkore/claude-statusline-patch/internal/targetlock"
	"github.com/leonardkore/claude-statusline-patch/internal/verifier"
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
		"verification_claim: " + legacyVerificationClaim(expectedClaim),
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
	if !strings.Contains(out, "verification_claim: not-live-verified") {
		t.Fatalf("expected legacy verification claim, got %q", out)
	}
	if !strings.Contains(out, "quick_apply_candidate: false") {
		t.Fatalf("expected quick_apply_candidate false, got %q", out)
	}
}

func TestFormatCheckOutputSanitizesVersionAndPath(t *testing.T) {
	t.Parallel()

	out := formatCheckOutput("/tmp/claude\nfake", patch.Inspection{
		State:      patch.StateUnrecognizedShape,
		ShapeState: patch.ShapeStateUnrecognized,
		PatchState: patch.PatchStateUnknown,
		Version:    "2.1.85\nquick_apply_candidate: true",
	}, false)

	if strings.Contains(out, "\nquick_apply_candidate: true\n") {
		t.Fatalf("expected newline injection to be escaped, got %q", out)
	}
	if !strings.Contains(out, `binary: /tmp/claude\nfake`) {
		t.Fatalf("expected sanitized binary path, got %q", out)
	}
	if !strings.Contains(out, `version: 2.1.85\nquick_apply_candidate: true`) {
		t.Fatalf("expected sanitized version, got %q", out)
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
	}, false, 1000, &patch.Inspection{
		State:      patch.StatePatched,
		PatchState: patch.PatchStatePatched,
		ShapeState: patch.ShapeStateKnown,
		ShapeID:    patch.ShapeIDStatuslineDebounceV1,
		IntervalMS: 1000,
	}, "ok", "passed", "")

	for _, fragment := range []string{
		"support_claim: patchable_only",
		"verification_claim: not-live-verified",
		"quick_apply_candidate: true",
		"current_state: unpatched",
		"dry_run: ok",
		"dry_run_rebuild_validation: passed",
		"simulated_state: patched",
		"simulated_interval_ms: 1000",
		"would_apply_interval_ms: 1000",
	} {
		if !strings.Contains(out, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, out)
		}
	}
}

func TestManifestSupportClaimsMatchRuntimeLogic(t *testing.T) {
	t.Parallel()

	for _, fixture := range loadManifest(t).Fixtures {
		fixture := fixture
		t.Run(fixture.ID, func(t *testing.T) {
			inspection := patch.Inspect(fixturePayload(t, fixture))
			want := expectedRuntimeSupportClaim(fixture, inspection)
			if got := supportClaim(inspection); got != want {
				t.Fatalf("expected support claim %s, got %s", want, got)
			}
		})
	}
}

func TestRunApplyDryRunOutputsValidationAndDoesNotWrite(t *testing.T) {
	tempDir := t.TempDir()
	setTestStateRoot(t)

	binaryPath := writeTestBinary(t, tempDir, "unpatched-2.1.85", fixturePayloadByID(t, "claude-2.1.85-unpatched"))
	original := mustReadFile(t, binaryPath)

	exitCode, stdout, stderr := captureRunApply(t, "--binary", binaryPath, "--dry-run", "--interval-ms", "1000")
	if exitCode != 0 {
		t.Fatalf("expected dry-run success, got exit %d stderr=%q", exitCode, stderr)
	}
	for _, fragment := range []string{
		"current_state: unpatched",
		"dry_run: ok",
		"dry_run_rebuild_validation: passed",
		"simulated_state: patched",
		"simulated_interval_ms: 1000",
		"would_apply_interval_ms: 1000",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("expected stdout to contain %q, got %q", fragment, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if got := mustReadFile(t, binaryPath); !bytes.Equal(got, original) {
		t.Fatalf("expected dry-run to leave binary unchanged")
	}

	targetDir, err := backup.TargetDir(binaryPath)
	if err != nil {
		t.Fatalf("TargetDir failed: %v", err)
	}
	if _, err := os.Stat(targetDir); err == nil {
		t.Fatalf("expected dry-run not to create backup state directory")
	} else if !os.IsNotExist(err) {
		t.Fatalf("expected target state dir to be absent, got %v", err)
	}
}

func TestRunApplyDryRunAlreadyPatchedManagedSameIntervalReportsOk(t *testing.T) {
	tempDir := t.TempDir()
	setTestStateRoot(t)

	originalPayload := fixturePayloadByID(t, "claude-2.1.85-unpatched")
	originalBinary := buildMinimalELFWithBunSection(t, buildSectionGraphPayload(originalPayload))
	patchedPayload, err := patch.Apply(originalPayload, 1000)
	if err != nil {
		t.Fatalf("patch.Apply failed: %v", err)
	}
	patchedBinary := buildMinimalELFWithBunSection(t, buildSectionGraphPayload(patchedPayload))

	binaryPath := filepath.Join(tempDir, "patched-managed")
	if err := os.WriteFile(binaryPath, patchedBinary, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	originalHash := backup.SHA256Bytes(originalBinary)
	patchedHash := backup.SHA256Bytes(patchedBinary)
	backupPath, _, err := backup.EnsureBackup(binaryPath, originalHash, originalBinary)
	if err != nil {
		t.Fatalf("EnsureBackup failed: %v", err)
	}
	if err := backup.SaveMetadata(backup.Metadata{
		CanonicalPath:   binaryPath,
		DisplayPath:     binaryPath,
		DetectedVersion: "2.1.85",
		OriginalSHA256:  originalHash,
		PatchedSHA256:   patchedHash,
		IntervalMS:      1000,
		BackupPath:      backupPath,
		FileMode:        0o755,
	}); err != nil {
		t.Fatalf("SaveMetadata failed: %v", err)
	}

	exitCode, stdout, stderr := captureRunApply(t, "--binary", binaryPath, "--dry-run", "--interval-ms", "1000")
	if exitCode != 0 {
		t.Fatalf("expected dry-run success, got exit %d stderr=%q", exitCode, stderr)
	}
	for _, fragment := range []string{
		"current_state: patched",
		"current_interval_ms: 1000",
		"dry_run: ok",
		"dry_run_rebuild_validation: skipped_already_patched",
		"dry_run_reason: already_patched_same_interval",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("expected stdout to contain %q, got %q", fragment, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
}

func TestRunApplyDryRunAlreadyPatchedDifferentIntervalReportsBlocked(t *testing.T) {
	tempDir := t.TempDir()
	setTestStateRoot(t)

	originalPayload := fixturePayloadByID(t, "claude-2.1.85-unpatched")
	originalBinary := buildMinimalELFWithBunSection(t, buildSectionGraphPayload(originalPayload))
	patchedPayload, err := patch.Apply(originalPayload, 1000)
	if err != nil {
		t.Fatalf("patch.Apply failed: %v", err)
	}
	patchedBinary := buildMinimalELFWithBunSection(t, buildSectionGraphPayload(patchedPayload))

	binaryPath := filepath.Join(tempDir, "patched-managed-different-interval")
	if err := os.WriteFile(binaryPath, patchedBinary, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	originalHash := backup.SHA256Bytes(originalBinary)
	patchedHash := backup.SHA256Bytes(patchedBinary)
	backupPath, _, err := backup.EnsureBackup(binaryPath, originalHash, originalBinary)
	if err != nil {
		t.Fatalf("EnsureBackup failed: %v", err)
	}
	if err := backup.SaveMetadata(backup.Metadata{
		CanonicalPath:   binaryPath,
		DisplayPath:     binaryPath,
		DetectedVersion: "2.1.85",
		OriginalSHA256:  originalHash,
		PatchedSHA256:   patchedHash,
		IntervalMS:      1000,
		BackupPath:      backupPath,
		FileMode:        0o755,
	}); err != nil {
		t.Fatalf("SaveMetadata failed: %v", err)
	}

	exitCode, stdout, stderr := captureRunApply(t, "--binary", binaryPath, "--dry-run", "--interval-ms", "1500")
	if exitCode != 1 {
		t.Fatalf("expected dry-run blocked exit 1, got %d stderr=%q", exitCode, stderr)
	}
	for _, fragment := range []string{
		"dry_run: blocked",
		"dry_run_reason: restore_required_for_interval_change current_interval_ms=1000",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("expected stdout to contain %q, got %q", fragment, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
}

func TestRunApplyDryRunUnrecognizedShapeReportsBlocked(t *testing.T) {
	tempDir := t.TempDir()
	setTestStateRoot(t)

	binaryPath := writeTestBinary(t, tempDir, "unrecognized-2.1.85", fixturePayloadByID(t, "negative-2.1.85-unrecognized-delay"))
	exitCode, stdout, stderr := captureRunApply(t, "--binary", binaryPath, "--dry-run", "--interval-ms", "1000")
	if exitCode != 1 {
		t.Fatalf("expected dry-run blocked exit 1, got %d stderr=%q", exitCode, stderr)
	}
	for _, fragment := range []string{
		"current_state: unrecognized_shape",
		"dry_run: blocked",
		"dry_run_reason: unrecognized_shape",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("expected stdout to contain %q, got %q", fragment, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
}

func TestRunApplyPreservesNewBackupWhenAtomicWriteFailsAfterCommit(t *testing.T) {
	tempDir := t.TempDir()
	setTestStateRoot(t)

	binaryPath := writeTestBinary(t, tempDir, "unpatched-2.1.85-late-failure", fixturePayloadByID(t, "claude-2.1.85-unpatched"))
	originalBytes := mustReadFile(t, binaryPath)
	originalHash := backup.SHA256Bytes(originalBytes)

	originalWriter := writeBinaryAtomically
	t.Cleanup(func() {
		writeBinaryAtomically = originalWriter
	})

	callCount := 0
	writeBinaryAtomically = func(path, expectedCurrentHash string, data []byte, mode os.FileMode) error {
		callCount++
		if callCount != 1 {
			return originalWriter(path, expectedCurrentHash, data, mode)
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatalf("simulate committed write: %v", err)
		}
		return repack.NewPostCommitError(errors.New("sync directory: simulated late failure"))
	}

	exitCode, stdout, stderr := captureRunApply(t, "--binary", binaryPath, "--interval-ms", "1000")
	if exitCode != 1 {
		t.Fatalf("expected apply failure, got exit %d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("expected empty stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "simulated late failure") {
		t.Fatalf("expected stderr to mention late failure, got %q", stderr)
	}
	if !strings.Contains(stderr, "A backup of the original binary was preserved at:") {
		t.Fatalf("expected stderr to include backup recovery hint, got %q", stderr)
	}

	backupPath, err := backup.ExpectedBackupPath(binaryPath, originalHash)
	if err != nil {
		t.Fatalf("ExpectedBackupPath failed: %v", err)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("expected backup to be preserved after late write failure: %v", err)
	}
	if !strings.Contains(stderr, backupPath) {
		t.Fatalf("expected stderr to include backup path %s, got %q", backupPath, stderr)
	}
}

func TestRunEnsureKnownUnpatchedAppliesAndVerifies(t *testing.T) {
	tempDir := t.TempDir()
	setTestStateRoot(t)

	originalVerifier := verifyCurrentBinary
	t.Cleanup(func() {
		verifyCurrentBinary = originalVerifier
	})
	verifyCurrentBinary = func(ctx context.Context, durationSeconds int) (verifier.Result, error) {
		return verifier.Result{
			Mode:                   "on",
			DurationSeconds:        durationSeconds,
			DistinctSessionSeconds: []int{0, 1, 2, 3, 4},
			Passed:                 true,
		}, nil
	}

	binaryPath := writeTestBinary(t, tempDir, "ensure-unpatched", fixturePayloadByID(t, "claude-2.1.85-unpatched"))

	exitCode, stdout, stderr := captureRunEnsure(t, "--binary", binaryPath)
	if exitCode != 0 {
		t.Fatalf("expected ensure success, got exit %d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	for _, fragment := range []string{
		"ensure_outcome: verified_success",
		"ensure_action: applied_and_verified",
		"verification_passed: true",
		"verification_distinct_session_seconds: 0, 1, 2, 3, 4",
		"DONE",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("expected stdout to contain %q, got %q", fragment, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}

	patchedBytes := mustReadFile(t, binaryPath)
	_, _, inspection, err := inspectBinary(patchedBytes)
	if err != nil {
		t.Fatalf("inspectBinary failed: %v", err)
	}
	if inspection.State != patch.StatePatched || inspection.IntervalMS != 1000 {
		t.Fatalf("expected patched binary at 1000ms, got %+v", inspection)
	}

	record, err := backup.LoadVerifiedOutcome(binaryPath, backup.SHA256Bytes(patchedBytes), 1000, runtime.GOOS, runtime.GOARCH, ensureVerifierContractVersion)
	if err != nil {
		t.Fatalf("LoadVerifiedOutcome failed: %v", err)
	}
	if record == nil {
		t.Fatalf("expected verified outcome record")
	}
}

func TestRunEnsureAlreadyVerifiedExactTupleSkipsVerifier(t *testing.T) {
	tempDir := t.TempDir()
	setTestStateRoot(t)

	originalVerifier := verifyCurrentBinary
	t.Cleanup(func() {
		verifyCurrentBinary = originalVerifier
	})
	verifyCurrentBinary = func(ctx context.Context, durationSeconds int) (verifier.Result, error) {
		t.Fatalf("verifyCurrentBinary should not be called on exact tuple match")
		return verifier.Result{}, nil
	}

	originalPayload := fixturePayloadByID(t, "claude-2.1.85-unpatched")
	originalBinary := buildMinimalELFWithBunSection(t, buildSectionGraphPayload(originalPayload))
	patchedPayload, err := patch.Apply(originalPayload, 1000)
	if err != nil {
		t.Fatalf("patch.Apply failed: %v", err)
	}
	patchedBinary := buildMinimalELFWithBunSection(t, buildSectionGraphPayload(patchedPayload))

	binaryPath := filepath.Join(tempDir, "ensure-already-verified")
	if err := os.WriteFile(binaryPath, patchedBinary, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	originalHash := backup.SHA256Bytes(originalBinary)
	patchedHash := backup.SHA256Bytes(patchedBinary)
	backupPath, _, err := backup.EnsureBackup(binaryPath, originalHash, originalBinary)
	if err != nil {
		t.Fatalf("EnsureBackup failed: %v", err)
	}
	if err := backup.SaveMetadata(backup.Metadata{
		CanonicalPath:   binaryPath,
		DisplayPath:     binaryPath,
		DetectedVersion: "2.1.85",
		OriginalSHA256:  originalHash,
		PatchedSHA256:   patchedHash,
		IntervalMS:      1000,
		BackupPath:      backupPath,
		FileMode:        0o755,
	}); err != nil {
		t.Fatalf("SaveMetadata failed: %v", err)
	}
	if err := backup.SaveVerifiedOutcome(backup.VerifiedOutcome{
		CanonicalPath:           binaryPath,
		InstalledSHA256:         patchedHash,
		IntervalMS:              1000,
		PlatformGOOS:            runtime.GOOS,
		PlatformGOARCH:          runtime.GOARCH,
		VerifierContractVersion: ensureVerifierContractVersion,
		DetectedVersion:         "2.1.85",
	}); err != nil {
		t.Fatalf("SaveVerifiedOutcome failed: %v", err)
	}

	exitCode, stdout, stderr := captureRunEnsure(t, "--binary", binaryPath)
	if exitCode != 0 {
		t.Fatalf("expected ensure success, got exit %d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stdout, "ensure_action: already_verified_exact_tuple") {
		t.Fatalf("expected exact tuple action, got %q", stdout)
	}
	if !strings.Contains(stdout, "verified_tuple_match: true") {
		t.Fatalf("expected verified tuple match, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
}

func TestRunEnsureUnknownShapeReturnsPatchUpdateRequired(t *testing.T) {
	tempDir := t.TempDir()
	setTestStateRoot(t)

	originalVerifier := verifyCurrentBinary
	t.Cleanup(func() {
		verifyCurrentBinary = originalVerifier
	})
	verifyCurrentBinary = func(ctx context.Context, durationSeconds int) (verifier.Result, error) {
		t.Fatalf("verifyCurrentBinary should not be called for unknown shape")
		return verifier.Result{}, nil
	}

	binaryPath := writeTestBinary(t, tempDir, "ensure-unknown", fixturePayloadByID(t, "negative-2.1.85-unrecognized-delay"))
	original := mustReadFile(t, binaryPath)

	exitCode, stdout, stderr := captureRunEnsure(t, "--binary", binaryPath)
	if exitCode != 1 {
		t.Fatalf("expected patch update required exit 1, got %d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stdout, "ensure_outcome: patch_update_required") || !strings.Contains(stdout, "ensure_reason: unrecognized_shape") {
		t.Fatalf("expected patch update required output, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if got := mustReadFile(t, binaryPath); !bytes.Equal(got, original) {
		t.Fatalf("expected unknown shape to leave binary unchanged")
	}
}

func TestRunEnsureAmbiguousShapeReturnsPatchUpdateRequired(t *testing.T) {
	tempDir := t.TempDir()
	setTestStateRoot(t)

	originalVerifier := verifyCurrentBinary
	t.Cleanup(func() {
		verifyCurrentBinary = originalVerifier
	})
	verifyCurrentBinary = func(ctx context.Context, durationSeconds int) (verifier.Result, error) {
		t.Fatalf("verifyCurrentBinary should not be called for ambiguous shape")
		return verifier.Result{}, nil
	}

	binaryPath := writeTestBinary(t, tempDir, "ensure-ambiguous", fixturePayloadByID(t, "negative-2.1.85-duplicate-unpatched"))

	exitCode, stdout, stderr := captureRunEnsure(t, "--binary", binaryPath)
	if exitCode != 1 {
		t.Fatalf("expected patch update required exit 1, got %d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stdout, "ensure_reason: ambiguous_shape") {
		t.Fatalf("expected ambiguous shape reason, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
}

func TestRunEnsureVerificationFailureRestoresMutatedBinary(t *testing.T) {
	tempDir := t.TempDir()
	setTestStateRoot(t)

	originalVerifier := verifyCurrentBinary
	t.Cleanup(func() {
		verifyCurrentBinary = originalVerifier
	})
	verifyCurrentBinary = func(ctx context.Context, durationSeconds int) (verifier.Result, error) {
		return verifier.Result{
			Mode:                   "on",
			DurationSeconds:        durationSeconds,
			DistinctSessionSeconds: []int{0},
			Passed:                 false,
		}, nil
	}

	binaryPath := writeTestBinary(t, tempDir, "ensure-restore", fixturePayloadByID(t, "claude-2.1.85-unpatched"))
	original := mustReadFile(t, binaryPath)

	exitCode, stdout, stderr := captureRunEnsure(t, "--binary", binaryPath)
	if exitCode != 1 {
		t.Fatalf("expected patch update required exit 1, got %d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	for _, fragment := range []string{
		"ensure_outcome: patch_update_required",
		"ensure_reason: live_verification_failed_after_apply",
		"mutated_this_run: true",
		"restored_this_run: true",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("expected stdout to contain %q, got %q", fragment, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if got := mustReadFile(t, binaryPath); !bytes.Equal(got, original) {
		t.Fatalf("expected binary to be restored after failed verification")
	}
	if record, err := backup.LoadMatchingRecord(binaryPath, backup.SHA256Bytes(original)); err != nil {
		t.Fatalf("LoadMatchingRecord failed: %v", err)
	} else if record != nil {
		t.Fatalf("expected restore path to remove managed metadata")
	}
}

func TestRunEnsureManagedPatchedUnverifiedRerunVerifiesExistingPatch(t *testing.T) {
	tempDir := t.TempDir()
	setTestStateRoot(t)

	originalVerifier := verifyCurrentBinary
	t.Cleanup(func() {
		verifyCurrentBinary = originalVerifier
	})
	verifyCurrentBinary = func(ctx context.Context, durationSeconds int) (verifier.Result, error) {
		return verifier.Result{
			Mode:                   "on",
			DurationSeconds:        durationSeconds,
			DistinctSessionSeconds: []int{0, 1, 2, 3, 4},
			Passed:                 true,
		}, nil
	}

	originalPayload := fixturePayloadByID(t, "claude-2.1.85-unpatched")
	originalBinary := buildMinimalELFWithBunSection(t, buildSectionGraphPayload(originalPayload))
	patchedPayload, err := patch.Apply(originalPayload, 1000)
	if err != nil {
		t.Fatalf("patch.Apply failed: %v", err)
	}
	patchedBinary := buildMinimalELFWithBunSection(t, buildSectionGraphPayload(patchedPayload))

	binaryPath := filepath.Join(tempDir, "ensure-rerun")
	if err := os.WriteFile(binaryPath, patchedBinary, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	originalHash := backup.SHA256Bytes(originalBinary)
	patchedHash := backup.SHA256Bytes(patchedBinary)
	backupPath, _, err := backup.EnsureBackup(binaryPath, originalHash, originalBinary)
	if err != nil {
		t.Fatalf("EnsureBackup failed: %v", err)
	}
	if err := backup.SaveMetadata(backup.Metadata{
		CanonicalPath:   binaryPath,
		DisplayPath:     binaryPath,
		DetectedVersion: "2.1.85",
		OriginalSHA256:  originalHash,
		PatchedSHA256:   patchedHash,
		IntervalMS:      1000,
		BackupPath:      backupPath,
		FileMode:        0o755,
	}); err != nil {
		t.Fatalf("SaveMetadata failed: %v", err)
	}

	exitCode, stdout, stderr := captureRunEnsure(t, "--binary", binaryPath)
	if exitCode != 0 {
		t.Fatalf("expected verify-existing-patch success, got %d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stdout, "ensure_action: verified_existing_patch") {
		t.Fatalf("expected verified existing patch action, got %q", stdout)
	}
	if strings.Contains(stdout, "mutated_this_run: true") {
		t.Fatalf("expected no mutation during re-entry verification, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}

	record, err := backup.LoadVerifiedOutcome(binaryPath, patchedHash, 1000, runtime.GOOS, runtime.GOARCH, ensureVerifierContractVersion)
	if err != nil {
		t.Fatalf("LoadVerifiedOutcome failed: %v", err)
	}
	if record == nil {
		t.Fatalf("expected verified outcome record after rerun verification")
	}
}

func TestRunEnsureUnmanagedPatchedReturnsOperatorInterventionRequired(t *testing.T) {
	tempDir := t.TempDir()
	setTestStateRoot(t)

	originalVerifier := verifyCurrentBinary
	t.Cleanup(func() {
		verifyCurrentBinary = originalVerifier
	})
	verifyCurrentBinary = func(ctx context.Context, durationSeconds int) (verifier.Result, error) {
		t.Fatalf("verifyCurrentBinary should not be called for unmanaged patched binary")
		return verifier.Result{}, nil
	}

	originalPayload := fixturePayloadByID(t, "claude-2.1.85-unpatched")
	patchedPayload, err := patch.Apply(originalPayload, 1000)
	if err != nil {
		t.Fatalf("patch.Apply failed: %v", err)
	}
	binaryPath := writeTestBinary(t, tempDir, "ensure-unmanaged-patched", patchedPayload)

	exitCode, stdout, stderr := captureRunEnsure(t, "--binary", binaryPath)
	if exitCode != 3 {
		t.Fatalf("expected operator intervention exit 3, got %d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stdout, "ensure_reason: unmanaged_patched") {
		t.Fatalf("expected unmanaged patched reason, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
}

func TestRunEnsureLockBusyReturnsOperatorInterventionRequired(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("lock busy test requires unix flock implementation")
	}

	tempDir := t.TempDir()
	setTestStateRoot(t)

	originalVerifier := verifyCurrentBinary
	t.Cleanup(func() {
		verifyCurrentBinary = originalVerifier
	})
	verifyCurrentBinary = func(ctx context.Context, durationSeconds int) (verifier.Result, error) {
		t.Fatalf("verifyCurrentBinary should not be called when lock is busy")
		return verifier.Result{}, nil
	}

	binaryPath := writeTestBinary(t, tempDir, "ensure-lock-busy", fixturePayloadByID(t, "claude-2.1.85-unpatched"))
	release, err := targetlock.Acquire(binaryPath)
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	defer func() {
		_ = release()
	}()

	exitCode, stdout, stderr := captureRunEnsure(t, "--binary", binaryPath)
	if exitCode != 3 {
		t.Fatalf("expected operator intervention exit 3, got %d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stdout, "ensure_reason: lock_busy") {
		t.Fatalf("expected lock busy reason, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
}

func TestRunEnsureVerifierUnavailableRestoresAndReturnsInconclusive(t *testing.T) {
	tempDir := t.TempDir()
	setTestStateRoot(t)

	originalVerifier := verifyCurrentBinary
	t.Cleanup(func() {
		verifyCurrentBinary = originalVerifier
	})
	verifyCurrentBinary = func(ctx context.Context, durationSeconds int) (verifier.Result, error) {
		return verifier.Result{}, verifier.ErrUnavailable
	}

	binaryPath := writeTestBinary(t, tempDir, "ensure-verifier-unavailable", fixturePayloadByID(t, "claude-2.1.85-unpatched"))
	original := mustReadFile(t, binaryPath)

	exitCode, stdout, stderr := captureRunEnsure(t, "--binary", binaryPath)
	if exitCode != 2 {
		t.Fatalf("expected verification inconclusive exit 2, got %d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stdout, "ensure_outcome: verification_inconclusive_or_unavailable") {
		t.Fatalf("expected inconclusive outcome, got %q", stdout)
	}
	if !strings.Contains(stdout, "restored_this_run: true") {
		t.Fatalf("expected restore on verifier unavailable after apply, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if got := mustReadFile(t, binaryPath); !bytes.Equal(got, original) {
		t.Fatalf("expected binary to be restored after verifier unavailable")
	}
}

type fixtureManifest struct {
	Fixtures []fixtureRecord `json:"fixtures"`
}

type fixtureRecord struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	Version       string `json:"version"`
	State         string `json:"state"`
	PatchState    string `json:"patch_state"`
	Authoritative bool   `json:"authoritative"`
	SupportClaim  string `json:"support_claim"`
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
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", fixture.Path))
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture.Path, err)
	}
	if fixture.Version == "" {
		return data
	}
	return append([]byte(`VERSION:"`+fixture.Version+`";`), data...)
}

func captureRunApply(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	os.Stdout = stdoutW
	os.Stderr = stderrW
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	exitCode := runApply(args)

	_ = stdoutW.Close()
	_ = stderrW.Close()

	stdoutBytes, _ := io.ReadAll(stdoutR)
	stderrBytes, _ := io.ReadAll(stderrR)
	_ = stdoutR.Close()
	_ = stderrR.Close()

	return exitCode, string(stdoutBytes), string(stderrBytes)
}

func captureRunEnsure(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	os.Stdout = stdoutW
	os.Stderr = stderrW
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	exitCode := runEnsure(args)

	_ = stdoutW.Close()
	_ = stderrW.Close()

	stdoutBytes, _ := io.ReadAll(stdoutR)
	stderrBytes, _ := io.ReadAll(stderrR)
	_ = stdoutR.Close()
	_ = stderrR.Close()

	return exitCode, string(stdoutBytes), string(stderrBytes)
}

func expectedRuntimeSupportClaim(fixture fixtureRecord, inspection patch.Inspection) string {
	switch fixture.SupportClaim {
	case "undocumented":
		return "undocumented"
	case "live_verified":
		if inspection.ShapeState == patch.ShapeStateKnown && inspection.PatchState != patch.PatchStateUnknown && runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
			return "live_verified"
		}
		if inspection.ShapeState == patch.ShapeStateKnown && inspection.PatchState != patch.PatchStateUnknown {
			return "patchable_only"
		}
		return "undocumented"
	default:
		return fixture.SupportClaim
	}
}

func writeTestBinary(t *testing.T, dir, name string, entryContents []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	image := buildMinimalELFWithBunSection(t, buildSectionGraphPayload(entryContents))
	if err := os.WriteFile(path, image, 0o755); err != nil {
		t.Fatalf("write test binary: %v", err)
	}
	return path
}

func setTestStateRoot(t *testing.T) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir failed: %v", err)
	}
	stateRoot, err := os.MkdirTemp(home, ".claude-statusline-patch-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(stateRoot)
	})
	t.Setenv("XDG_STATE_HOME", stateRoot)
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return data
}

const (
	testOverlayTrailer = "\n---- Bun! ----\n"
	testOverlayOffsets = 32
	testModuleSize     = 52
)

func buildSectionGraphPayload(contents []byte) []byte {
	name := []byte("/$bunfs/root/src/entrypoints/cli.js")
	origin := []byte("/$bunfs/root/src/entrypoints/cli.js")
	graph := make([]byte, 0, len(name)+len(contents)+len(origin)+testModuleSize)

	namePtr := bun.StringPointer{Offset: uint32(len(graph)), Length: uint32(len(name))}
	graph = append(graph, name...)
	contentsPtr := bun.StringPointer{Offset: uint32(len(graph)), Length: uint32(len(contents))}
	graph = append(graph, contents...)
	originPtr := bun.StringPointer{Offset: uint32(len(graph)), Length: uint32(len(origin))}
	graph = append(graph, origin...)

	modulesPtr := bun.StringPointer{Offset: uint32(len(graph)), Length: testModuleSize}
	module := bun.Module{
		Name:               namePtr,
		Contents:           contentsPtr,
		BytecodeOriginPath: originPtr,
	}
	graph = append(graph, encodeModules([]bun.Module{module})...)

	offsetBytes := make([]byte, testOverlayOffsets)
	binary.LittleEndian.PutUint64(offsetBytes[:8], uint64(len(graph)))
	encodePointer(offsetBytes[8:16], modulesPtr)
	binary.LittleEndian.PutUint32(offsetBytes[16:20], 0)
	encodePointer(offsetBytes[20:28], bun.StringPointer{Offset: uint32(len(graph)), Length: 0})
	binary.LittleEndian.PutUint32(offsetBytes[28:32], 0)

	var out bytes.Buffer
	out.Write(graph)
	out.Write(offsetBytes)
	out.WriteString(testOverlayTrailer)
	return out.Bytes()
}

func encodeModules(modules []bun.Module) []byte {
	out := make([]byte, len(modules)*testModuleSize)
	for i, module := range modules {
		encoded := out[i*testModuleSize : (i+1)*testModuleSize]
		encodePointer(encoded[0:8], module.Name)
		encodePointer(encoded[8:16], module.Contents)
		encodePointer(encoded[16:24], module.Sourcemap)
		encodePointer(encoded[24:32], module.Bytecode)
		encodePointer(encoded[32:40], module.ModuleInfo)
		encodePointer(encoded[40:48], module.BytecodeOriginPath)
		encoded[48] = module.Encoding
		encoded[49] = module.Loader
		encoded[50] = module.ModuleFormat
		encoded[51] = module.Side
	}
	return out
}

func encodePointer(dst []byte, pointer bun.StringPointer) {
	binary.LittleEndian.PutUint32(dst[0:4], pointer.Offset)
	binary.LittleEndian.PutUint32(dst[4:8], pointer.Length)
}

func buildMinimalELFWithBunSection(t *testing.T, payload []byte) []byte {
	t.Helper()

	const (
		elfHeaderSize     = 64
		sectionHeaderSize = 64
		sectionCount      = 3
	)

	shstrtab := []byte{0, '.', 's', 'h', 's', 't', 'r', 't', 'a', 'b', 0, '.', 'b', 'u', 'n', 0}
	bunData := make([]byte, 8+len(payload))
	binary.LittleEndian.PutUint64(bunData[:8], uint64(len(payload)))
	copy(bunData[8:], payload)

	shstrtabOffset := elfHeaderSize
	bunOffset := alignUp(shstrtabOffset+len(shstrtab), 8)
	sectionHeadersOffset := alignUp(bunOffset+len(bunData), 8)
	totalSize := sectionHeadersOffset + (sectionHeaderSize * sectionCount)

	out := make([]byte, totalSize)
	copy(out[:4], []byte{0x7f, 'E', 'L', 'F'})
	out[4] = 2
	out[5] = 1
	out[6] = 1

	binary.LittleEndian.PutUint16(out[16:], 2)
	binary.LittleEndian.PutUint16(out[18:], 62)
	binary.LittleEndian.PutUint32(out[20:], 1)
	binary.LittleEndian.PutUint64(out[40:], uint64(sectionHeadersOffset))
	binary.LittleEndian.PutUint16(out[52:], elfHeaderSize)
	binary.LittleEndian.PutUint16(out[58:], sectionHeaderSize)
	binary.LittleEndian.PutUint16(out[60:], sectionCount)
	binary.LittleEndian.PutUint16(out[62:], 1)

	copy(out[shstrtabOffset:], shstrtab)
	copy(out[bunOffset:], bunData)

	shoff := sectionHeadersOffset
	writeSectionHeader(out[shoff+sectionHeaderSize:], 1, 3, 0, uint64(shstrtabOffset), uint64(len(shstrtab)), 1)
	writeSectionHeader(out[shoff+(sectionHeaderSize*2):], 11, 1, 0, uint64(bunOffset), uint64(len(bunData)), 1)

	return out
}

func writeSectionHeader(dst []byte, nameOffset uint32, sectionType uint32, flags uint64, offset uint64, size uint64, align uint64) {
	binary.LittleEndian.PutUint32(dst[0:], nameOffset)
	binary.LittleEndian.PutUint32(dst[4:], sectionType)
	binary.LittleEndian.PutUint64(dst[8:], flags)
	binary.LittleEndian.PutUint64(dst[24:], offset)
	binary.LittleEndian.PutUint64(dst[32:], size)
	binary.LittleEndian.PutUint64(dst[48:], align)
}

func alignUp(value int, align int) int {
	if value%align == 0 {
		return value
	}
	return value + (align - (value % align))
}
