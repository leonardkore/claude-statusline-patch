package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/leonardkore/claude-statusline-patch/internal/backup"
	"github.com/leonardkore/claude-statusline-patch/internal/bun"
	"github.com/leonardkore/claude-statusline-patch/internal/claude"
	"github.com/leonardkore/claude-statusline-patch/internal/patch"
	"github.com/leonardkore/claude-statusline-patch/internal/repack"
	"github.com/leonardkore/claude-statusline-patch/internal/targetlock"
	"github.com/leonardkore/claude-statusline-patch/internal/verifier"
)

const ensureVerifierContractVersion = 1

var (
	acquireEnsureLock   = targetlock.Acquire
	verifyCurrentBinary = func(ctx context.Context, targetBinary string, contractVersion, durationSeconds int) (verifier.Result, error) {
		return verifier.VerifyWithOptions(ctx, verifier.Options{
			Mode:            "on",
			DurationSeconds: durationSeconds,
			TargetBinary:    targetBinary,
			ContractVersion: contractVersion,
		})
	}
	verifyTargetMatchesActive = targetMatchesActiveClaude
)

type ensureOutcome string

const (
	ensureOutcomeVerifiedSuccess                   ensureOutcome = "verified_success"
	ensureOutcomePatchUpdateRequired               ensureOutcome = "patch_update_required"
	ensureOutcomeVerificationInconclusiveAvailable ensureOutcome = "verification_inconclusive_or_unavailable"
	ensureOutcomeOperatorInterventionRequired      ensureOutcome = "operator_intervention_required"
	ensureOutcomeLocalError                        ensureOutcome = "local_error"
)

type ensureResult struct {
	BinaryPath        string
	Version           string
	Inspection        patch.Inspection
	Managed           bool
	SupportClaim      string
	VerificationClaim string
	QuickApply        bool
	IntervalMS        int
	Outcome           ensureOutcome
	Reason            string
	Action            string
	VerifiedTuple     bool
	Mutated           bool
	Restored          bool
	VerifyDuration    int
	VerifyResult      *verifier.Result
}

type ensureState struct {
	resolved   *claude.ResolvedBinary
	bytes      []byte
	hash       string
	bundle     *bun.Bundle
	graph      *bun.ModuleGraph
	inspection patch.Inspection
	managed    *backup.Metadata
}

func runEnsure(args []string) int {
	fs := flag.NewFlagSet("ensure", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	binaryPath := fs.String("binary", "", "path to the Claude binary (defaults to ~/.local/bin/claude)")
	intervalMS := fs.Int("interval-ms", 1000, "fixed statusline refresh interval in milliseconds")
	verifySeconds := fs.Int("verify-seconds", verifier.DefaultDurationSeconds, "live verification sample duration in seconds")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *intervalMS <= 0 {
		fmt.Fprintln(os.Stderr, "interval must be positive")
		return 2
	}
	if *verifySeconds <= 0 {
		fmt.Fprintln(os.Stderr, "verify-seconds must be positive")
		return 2
	}

	resolved, err := claude.Resolve(*binaryPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ensureOutcomeLocalError.exitCode()
	}

	release, err := acquireEnsureLock(resolved.CanonicalPath)
	if err != nil {
		result := ensureResult{
			BinaryPath:     resolved.CanonicalPath,
			IntervalMS:     *intervalMS,
			VerifyDuration: *verifySeconds,
			Outcome:        ensureOutcomeOperatorInterventionRequired,
			Reason:         "lock_busy",
		}
		if !errors.Is(err, targetlock.ErrBusy) {
			result.Outcome = ensureOutcomeLocalError
			result.Reason = "lock_error"
		}
		fmt.Print(formatEnsureOutput(result))
		return result.Outcome.exitCode()
	}
	defer func() {
		_ = release()
	}()

	state, err := loadEnsureStateFromResolved(resolved)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return ensureOutcomeLocalError.exitCode()
	}

	result := baseEnsureResult(state, *intervalMS)
	result.VerifyDuration = *verifySeconds
	record, recordErr := loadExactVerifiedOutcome(state, *intervalMS)
	if recordErr != nil {
		result.Outcome = ensureOutcomeLocalError
		result.Reason = "verified_state_load_failed"
		fmt.Print(formatEnsureOutput(result))
		fmt.Fprintln(os.Stderr, recordErr)
		return result.Outcome.exitCode()
	}

	managedPatched := state.managed != nil && state.managed.PatchedSHA256 == state.hash
	exactVerifiedTuple := record != nil && state.inspection.State == patch.StatePatched && managedPatched && state.inspection.IntervalMS == *intervalMS
	if exactVerifiedTuple {
		result.VerifiedTuple = true
	}

	switch state.inspection.State {
	case patch.StateAmbiguousShape:
		result.Outcome = ensureOutcomePatchUpdateRequired
		result.Reason = "ambiguous_shape"
		fmt.Print(formatEnsureOutput(result))
		return result.Outcome.exitCode()
	case patch.StateUnrecognizedShape:
		result.Outcome = ensureOutcomePatchUpdateRequired
		result.Reason = "unrecognized_shape"
		fmt.Print(formatEnsureOutput(result))
		return result.Outcome.exitCode()
	case patch.StatePatched:
		return runEnsurePatched(state, result, *intervalMS, *verifySeconds)
	case patch.StateUnpatched:
		return runEnsureUnpatched(state, result, *intervalMS, *verifySeconds)
	default:
		result.Outcome = ensureOutcomeLocalError
		result.Reason = "unknown_state"
		fmt.Print(formatEnsureOutput(result))
		return result.Outcome.exitCode()
	}
}

func runEnsurePatched(state *ensureState, result ensureResult, intervalMS, verifySeconds int) int {
	if state.managed == nil || state.managed.PatchedSHA256 != state.hash {
		result.Outcome = ensureOutcomeOperatorInterventionRequired
		result.Reason = "unmanaged_patched"
		fmt.Print(formatEnsureOutput(result))
		return result.Outcome.exitCode()
	}
	if state.inspection.IntervalMS != intervalMS {
		result.Outcome = ensureOutcomeOperatorInterventionRequired
		result.Reason = "interval_change_requires_restore"
		fmt.Print(formatEnsureOutput(result))
		return result.Outcome.exitCode()
	}
	if err := checkVerifierTarget(state.resolved.CanonicalPath); err != nil {
		result.Outcome = ensureVerificationOutcome(err)
		result.Reason = ensureVerificationReason(err)
		fmt.Print(formatEnsureOutput(result))
		fmt.Fprintln(os.Stderr, err)
		return result.Outcome.exitCode()
	}

	verifyResult, verifyErr := verifyPatchedBinary(state.resolved.CanonicalPath, verifySeconds)
	if verifyErr != nil {
		result.Outcome = ensureVerificationOutcome(verifyErr)
		result.Reason = ensureVerificationReason(verifyErr)
		if restoreErr := restoreResolvedBinary(state.resolved); restoreErr != nil {
			result.Outcome = ensureOutcomeLocalError
			result.Reason = "restore_failed_after_existing_patch_verification_error"
			fmt.Print(formatEnsureOutput(result))
			fmt.Fprintln(os.Stderr, restoreErr)
			return result.Outcome.exitCode()
		}
		result.Restored = true
		fmt.Print(formatEnsureOutput(result))
		fmt.Fprintln(os.Stderr, verifyErr)
		return result.Outcome.exitCode()
	}
	result.VerifyResult = &verifyResult
	if !verifyResult.Passed {
		result.Outcome = ensureOutcomePatchUpdateRequired
		result.Reason = "live_verification_failed_existing_patch"
		if restoreErr := restoreResolvedBinary(state.resolved); restoreErr != nil {
			result.Outcome = ensureOutcomeLocalError
			result.Reason = "restore_failed_after_existing_patch_verification_failure"
			fmt.Print(formatEnsureOutput(result))
			fmt.Fprintln(os.Stderr, restoreErr)
			return result.Outcome.exitCode()
		}
		result.Restored = true
		fmt.Print(formatEnsureOutput(result))
		return result.Outcome.exitCode()
	}

	if err := saveVerifiedOutcome(state, intervalMS, verifyResult); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	result.Outcome = ensureOutcomeVerifiedSuccess
	result.Action = "verified_existing_patch"
	fmt.Print(formatEnsureOutput(result))
	return result.Outcome.exitCode()
}

func runEnsureUnpatched(state *ensureState, result ensureResult, intervalMS, verifySeconds int) int {
	if !canLiveVerifyCurrentPlatform() {
		result.Outcome = ensureOutcomeVerificationInconclusiveAvailable
		result.Reason = "live_verification_unsupported_platform"
		fmt.Print(formatEnsureOutput(result))
		return result.Outcome.exitCode()
	}
	if err := checkVerifierTarget(state.resolved.CanonicalPath); err != nil {
		result.Outcome = ensureVerificationOutcome(err)
		result.Reason = ensureVerificationReason(err)
		fmt.Print(formatEnsureOutput(result))
		fmt.Fprintln(os.Stderr, err)
		return result.Outcome.exitCode()
	}

	patchedBytes, _, err := rebuildPatchedBinary(state.bytes, state.bundle, state.graph, state.inspection, intervalMS)
	if err != nil {
		result.Outcome = ensureOutcomeLocalError
		result.Reason = "dry_run_rebuild_validation_failed"
		fmt.Print(formatEnsureOutput(result))
		fmt.Fprintln(os.Stderr, err)
		return result.Outcome.exitCode()
	}

	patchedHash, err := applyPatchedBinary(state, patchedBytes, intervalMS)
	if err != nil {
		result.Outcome = ensureOutcomeLocalError
		result.Reason = "apply_failed"
		fmt.Print(formatEnsureOutput(result))
		fmt.Fprintln(os.Stderr, err)
		return result.Outcome.exitCode()
	}
	result.Mutated = true

	verifyResult, verifyErr := verifyPatchedBinary(state.resolved.CanonicalPath, verifySeconds)
	if verifyErr != nil {
		result.Outcome = ensureVerificationOutcome(verifyErr)
		result.Reason = ensureVerificationReason(verifyErr) + "_after_apply"
		if restoreErr := restoreResolvedBinary(state.resolved); restoreErr != nil {
			result.Outcome = ensureOutcomeLocalError
			result.Reason = "restore_failed_after_verification_error"
			fmt.Print(formatEnsureOutput(result))
			fmt.Fprintln(os.Stderr, restoreErr)
			return result.Outcome.exitCode()
		}
		result.Restored = true
		fmt.Print(formatEnsureOutput(result))
		fmt.Fprintln(os.Stderr, verifyErr)
		return result.Outcome.exitCode()
	}
	result.VerifyResult = &verifyResult
	if !verifyResult.Passed {
		result.Outcome = ensureOutcomePatchUpdateRequired
		result.Reason = "live_verification_failed_after_apply"
		if restoreErr := restoreResolvedBinary(state.resolved); restoreErr != nil {
			result.Outcome = ensureOutcomeLocalError
			result.Reason = "restore_failed_after_live_verification_failure"
			fmt.Print(formatEnsureOutput(result))
			fmt.Fprintln(os.Stderr, restoreErr)
			return result.Outcome.exitCode()
		}
		result.Restored = true
		fmt.Print(formatEnsureOutput(result))
		return result.Outcome.exitCode()
	}

	state.hash = patchedHash
	state.inspection = patch.Inspection{
		State:            patch.StatePatched,
		ShapeState:       state.inspection.ShapeState,
		PatchState:       patch.PatchStatePatched,
		Version:          state.inspection.Version,
		ShapeID:          state.inspection.ShapeID,
		ObservedVersions: append([]string(nil), state.inspection.ObservedVersions...),
		IntervalMS:       intervalMS,
	}
	if recordErr := saveVerifiedOutcome(state, intervalMS, verifyResult); recordErr != nil {
		fmt.Fprintln(os.Stderr, recordErr)
	}
	result.Outcome = ensureOutcomeVerifiedSuccess
	result.Action = "applied_and_verified"
	fmt.Print(formatEnsureOutput(result))
	return result.Outcome.exitCode()
}

func loadEnsureState(binaryPath string) (*ensureState, error) {
	resolved, err := claude.Resolve(binaryPath)
	if err != nil {
		return nil, err
	}
	return loadEnsureStateFromResolved(resolved)
}

func loadEnsureStateFromResolved(resolved *claude.ResolvedBinary) (*ensureState, error) {
	data, err := readBoundedFile(resolved.CanonicalPath, maxBinarySizeBytes)
	if err != nil {
		return nil, err
	}
	hash := backup.SHA256Bytes(data)
	bundle, graph, inspection, err := inspectBinary(data)
	if err != nil {
		return nil, err
	}
	managed, err := backup.LoadMatchingRecord(resolved.CanonicalPath, hash)
	if err != nil {
		return nil, err
	}
	return &ensureState{
		resolved:   resolved,
		bytes:      data,
		hash:       hash,
		bundle:     bundle,
		graph:      graph,
		inspection: inspection,
		managed:    managed,
	}, nil
}

func baseEnsureResult(state *ensureState, intervalMS int) ensureResult {
	return ensureResult{
		BinaryPath:        state.resolved.CanonicalPath,
		Version:           state.inspection.Version,
		Inspection:        state.inspection,
		Managed:           state.managed != nil,
		SupportClaim:      supportClaim(state.inspection),
		VerificationClaim: legacyVerificationClaim(supportClaim(state.inspection)),
		QuickApply:        quickApplyCandidate(state.inspection),
		IntervalMS:        intervalMS,
		VerifyDuration:    verifier.DefaultDurationSeconds,
	}
}

func applyPatchedBinary(state *ensureState, patchedBytes []byte, intervalMS int) (string, error) {
	patchedHash := backup.SHA256Bytes(patchedBytes)
	backupPath, backupCreated, err := backup.EnsureBackup(state.resolved.CanonicalPath, state.hash, state.bytes)
	if err != nil {
		return "", err
	}
	cleanupBackup := func() {
		if backupCreated {
			_ = backup.DeleteBackup(state.resolved.CanonicalPath, state.hash)
		}
	}

	if err := writeBinaryAtomically(state.resolved.CanonicalPath, state.hash, patchedBytes, state.resolved.Mode); err != nil {
		if !repack.TargetMayHaveChanged(err) {
			cleanupBackup()
		}
		if repack.TargetMayHaveChanged(err) {
			return "", preservedBackupError(err, backupPath, state.resolved.CanonicalPath)
		}
		return "", err
	}

	if err := backup.SaveMetadata(backup.Metadata{
		CanonicalPath:   state.resolved.CanonicalPath,
		DisplayPath:     state.resolved.DisplayPath,
		DetectedVersion: state.inspection.Version,
		OriginalSHA256:  state.hash,
		PatchedSHA256:   patchedHash,
		IntervalMS:      intervalMS,
		BackupPath:      backupPath,
		FileMode:        uint32(state.resolved.Mode.Perm()),
	}); err != nil {
		rollbackErr := writeBinaryAtomically(state.resolved.CanonicalPath, patchedHash, state.bytes, state.resolved.Mode)
		if rollbackErr != nil {
			return "", fmt.Errorf("save metadata: %v; rollback failed: %w", err, rollbackErr)
		}
		cleanupBackup()
		return "", fmt.Errorf("save metadata: %w", err)
	}
	return patchedHash, nil
}

func restoreResolvedBinary(resolved *claude.ResolvedBinary) error {
	currentBytes, err := readBoundedFile(resolved.CanonicalPath, maxBinarySizeBytes)
	if err != nil {
		return err
	}
	currentHash := backup.SHA256Bytes(currentBytes)
	managed, err := backup.LoadMatchingRecord(resolved.CanonicalPath, currentHash)
	if err != nil {
		return err
	}
	if managed == nil {
		return errors.New("no managed backup record found for this binary")
	}
	if currentHash != managed.PatchedSHA256 {
		if currentHash == managed.OriginalSHA256 {
			if err := cleanupManagedState(managed); err != nil {
				return err
			}
			return nil
		}
		return errors.New("binary no longer matches the managed patched hash; refusing restore")
	}

	backupPath, err := backup.ExpectedBackupPath(managed.CanonicalPath, managed.OriginalSHA256)
	if err != nil {
		return err
	}
	backupBytes, err := readBoundedFile(backupPath, maxBinarySizeBytes)
	if err != nil {
		return err
	}
	backupHash := backup.SHA256Bytes(backupBytes)
	if backupHash != managed.OriginalSHA256 {
		return fmt.Errorf("managed backup hash mismatch: expected %s, found %s", managed.OriginalSHA256, backupHash)
	}
	mode := os.FileMode(managed.FileMode)
	if mode == 0 {
		mode = resolved.Mode
	}
	if err := writeBinaryAtomically(resolved.CanonicalPath, currentHash, backupBytes, mode); err != nil {
		return err
	}
	return cleanupManagedState(managed)
}

func loadExactVerifiedOutcome(state *ensureState, intervalMS int) (*backup.VerifiedOutcome, error) {
	return backup.LoadVerifiedOutcome(
		state.resolved.CanonicalPath,
		state.hash,
		intervalMS,
		runtime.GOOS,
		runtime.GOARCH,
		ensureVerifierContractVersion,
	)
}

func saveVerifiedOutcome(state *ensureState, intervalMS int, verifyResult verifier.Result) error {
	return backup.SaveVerifiedOutcome(backup.VerifiedOutcome{
		CanonicalPath:           state.resolved.CanonicalPath,
		InstalledSHA256:         state.hash,
		IntervalMS:              intervalMS,
		PlatformGOOS:            runtime.GOOS,
		PlatformGOARCH:          runtime.GOARCH,
		VerifierContractVersion: ensureVerifierContractVersion,
		DetectedVersion:         state.inspection.Version,
		VerifierRunID:           verifyResult.RunID,
		EventsFile:              verifyResult.EventsFile,
		PaneCaptureFile:         verifyResult.PaneCaptureFile,
		DistinctSessionSeconds:  append([]int(nil), verifyResult.DistinctSessionSeconds...),
	})
}

func verifyPatchedBinary(canonicalPath string, verifySeconds int) (verifier.Result, error) {
	if err := checkVerifierTarget(canonicalPath); err != nil {
		return verifier.Result{}, err
	}
	timeout := time.Duration(verifySeconds+30) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return verifyCurrentBinary(ctx, canonicalPath, ensureVerifierContractVersion, verifySeconds)
}

func checkVerifierTarget(canonicalPath string) error {
	matches, err := verifyTargetMatchesActive(canonicalPath)
	if err != nil {
		return err
	}
	if !matches {
		return verifier.ErrTargetMismatch
	}
	return nil
}

func canLiveVerifyCurrentPlatform() bool {
	return runtime.GOOS == "linux" && runtime.GOARCH == "amd64"
}

func ensureVerificationOutcome(err error) ensureOutcome {
	if errors.Is(err, verifier.ErrUnavailable) {
		return ensureOutcomeVerificationInconclusiveAvailable
	}
	if errors.Is(err, verifier.ErrTargetMismatch) {
		return ensureOutcomeOperatorInterventionRequired
	}
	return ensureOutcomeVerificationInconclusiveAvailable
}

func ensureVerificationReason(err error) string {
	switch {
	case errors.Is(err, verifier.ErrUnavailable):
		return "verifier_unavailable"
	case errors.Is(err, verifier.ErrTargetMismatch):
		return "verifier_target_mismatch"
	case errors.Is(err, context.DeadlineExceeded):
		return "verifier_timeout"
	default:
		return "verifier_error"
	}
}

func targetMatchesActiveClaude(canonicalPath string) (bool, error) {
	active, err := claude.Resolve("")
	if err != nil {
		return false, err
	}
	return active.CanonicalPath == canonicalPath, nil
}

func cleanupManagedState(managed *backup.Metadata) error {
	if err := backup.DeleteVerifiedOutcomes(managed.CanonicalPath); err != nil {
		return err
	}
	if err := backup.DeleteMetadata(managed.CanonicalPath, managed.OriginalSHA256); err != nil {
		return err
	}
	return backup.DeleteBackup(managed.CanonicalPath, managed.OriginalSHA256)
}

func preservedBackupError(err error, backupPath, canonicalPath string) error {
	return fmt.Errorf("%w\n\nThe target binary may already have been modified after patching.\nA backup of the original binary was preserved at:\n  %s\nUse `claude-statusline-patch restore --binary %s` if the binary still matches the managed patched hash, or restore the backup manually from that path if needed.", err, backupPath, canonicalPath)
}

func formatEnsureOutput(result ensureResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "binary: %s\n", sanitizeOutputValue(result.BinaryPath))
	fmt.Fprintf(&b, "version: %s\n", sanitizeOutputValue(result.Version))
	fmt.Fprintf(&b, "state: %s\n", result.Inspection.State)
	if result.Inspection.ShapeState == patch.ShapeStateKnown {
		fmt.Fprintf(&b, "shape_id: %s\n", result.Inspection.ShapeID)
		fmt.Fprintf(&b, "observed_versions: %s\n", strings.Join(result.Inspection.ObservedVersions, ", "))
	}
	fmt.Fprintf(&b, "shape_state: %s\n", result.Inspection.ShapeState)
	fmt.Fprintf(&b, "patch_state: %s\n", result.Inspection.PatchState)
	if result.Inspection.State == patch.StatePatched {
		fmt.Fprintf(&b, "current_interval_ms: %d\n", result.Inspection.IntervalMS)
	}
	fmt.Fprintf(&b, "support_claim: %s\n", result.SupportClaim)
	fmt.Fprintf(&b, "verification_claim: %s\n", result.VerificationClaim)
	fmt.Fprintf(&b, "quick_apply_candidate: %t\n", result.QuickApply)
	fmt.Fprintf(&b, "managed: %t\n", result.Managed)
	fmt.Fprintf(&b, "desired_interval_ms: %d\n", result.IntervalMS)
	fmt.Fprintf(&b, "verified_tuple_match: %t\n", result.VerifiedTuple)
	fmt.Fprintf(&b, "ensure_outcome: %s\n", result.Outcome)
	if result.Action != "" {
		fmt.Fprintf(&b, "ensure_action: %s\n", result.Action)
	}
	if result.Reason != "" {
		fmt.Fprintf(&b, "ensure_reason: %s\n", sanitizeOutputValue(result.Reason))
	}
	fmt.Fprintf(&b, "mutated_this_run: %t\n", result.Mutated)
	fmt.Fprintf(&b, "restored_this_run: %t\n", result.Restored)
	fmt.Fprintf(&b, "verifier_contract_version: %d\n", ensureVerifierContractVersion)
	if result.VerifyResult != nil {
		fmt.Fprintf(&b, "verification_mode: %s\n", sanitizeOutputValue(result.VerifyResult.Mode))
		fmt.Fprintf(&b, "verification_duration_seconds: %d\n", result.VerifyResult.DurationSeconds)
		fmt.Fprintf(&b, "verification_passed: %t\n", result.VerifyResult.Passed)
		fmt.Fprintf(&b, "verification_distinct_session_seconds: %s\n", joinInts(result.VerifyResult.DistinctSessionSeconds))
	}
	if result.Outcome == ensureOutcomeVerifiedSuccess {
		fmt.Fprintf(&b, "DONE\n")
	}
	return b.String()
}

func joinInts(values []int) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%d", value))
	}
	return strings.Join(parts, ", ")
}

func (outcome ensureOutcome) exitCode() int {
	switch outcome {
	case ensureOutcomeVerifiedSuccess:
		return 0
	case ensureOutcomePatchUpdateRequired:
		return 1
	case ensureOutcomeVerificationInconclusiveAvailable:
		return 2
	case ensureOutcomeOperatorInterventionRequired:
		return 3
	default:
		return 4
	}
}
