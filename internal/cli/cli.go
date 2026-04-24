package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/leonardkore/claude-statusline-patch/internal/backup"
	"github.com/leonardkore/claude-statusline-patch/internal/bun"
	"github.com/leonardkore/claude-statusline-patch/internal/claude"
	"github.com/leonardkore/claude-statusline-patch/internal/fileutil"
	"github.com/leonardkore/claude-statusline-patch/internal/patch"
	"github.com/leonardkore/claude-statusline-patch/internal/repack"
	"github.com/leonardkore/claude-statusline-patch/internal/targetlock"
	"github.com/leonardkore/claude-statusline-patch/internal/version"
)

const maxBinarySizeBytes int64 = 1 << 30

var writeBinaryAtomically = repack.WriteAtomically

func Main(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return 2
	}

	switch args[0] {
	case "ensure":
		return runEnsure(args[1:])
	case "apply":
		return runApply(args[1:])
	case "check":
		return runCheck(args[1:])
	case "restore":
		return runRestore(args[1:])
	case "version":
		fmt.Println(version.String())
		return 0
	default:
		printUsage(os.Stderr)
		return 2
	}
}

func runApply(args []string) int {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	binaryPath := fs.String("binary", "", "path to the Claude binary (defaults to ~/.local/bin/claude)")
	intervalMS := fs.Int("interval-ms", 1000, "fixed statusline refresh interval in milliseconds")
	dryRun := fs.Bool("dry-run", false, "validate the patch/rebuild path without mutating the binary")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *intervalMS <= 0 {
		fmt.Fprintln(os.Stderr, "interval must be positive")
		return 2
	}

	resolved, err := claude.Resolve(*binaryPath)
	if err != nil {
		return fail(err)
	}
	release, err := acquireEnsureLock(resolved.CanonicalPath)
	if err != nil {
		return fail(lockError(err))
	}
	defer func() {
		_ = release()
	}()

	originalBytes, err := readBoundedFile(resolved.CanonicalPath, maxBinarySizeBytes)
	if err != nil {
		return fail(err)
	}
	originalHash := backup.SHA256Bytes(originalBytes)

	bundle, graph, inspection, err := inspectBinary(originalBytes)
	if err != nil {
		return fail(err)
	}
	managed, err := backup.LoadMatchingRecord(resolved.CanonicalPath, originalHash)
	if err != nil {
		return fail(err)
	}

	if *dryRun {
		return runApplyDryRun(resolved.CanonicalPath, originalHash, originalBytes, bundle, graph, inspection, managed, *intervalMS)
	}

	switch inspection.State {
	case patch.StatePatched:
		if managed == nil || managed.PatchedSHA256 != originalHash {
			return fail(errors.New("binary is already patched but unmanaged; run restore from a managed binary or start from a clean Claude install"))
		}
		if inspection.IntervalMS == *intervalMS {
			fmt.Printf("already patched: %s interval=%dms\n", resolved.CanonicalPath, inspection.IntervalMS)
			return 0
		}
		return fail(fmt.Errorf("binary is already patched at %dms; run restore before applying a different interval", inspection.IntervalMS))
	case patch.StateAmbiguousShape:
		return fail(errors.New("refusing to patch: statusline shape match is ambiguous"))
	case patch.StateUnrecognizedShape:
		return fail(fmt.Errorf("refusing to patch unrecognized statusline shape for Claude version %q", inspection.Version))
	case patch.StateUnpatched:
		// continue
	default:
		return fail(fmt.Errorf("refusing to patch unknown state %q", inspection.State))
	}

	patchedBytes, _, err := rebuildPatchedBinary(originalBytes, bundle, graph, inspection, *intervalMS)
	if err != nil {
		return fail(err)
	}
	patchedHash := backup.SHA256Bytes(patchedBytes)

	backupPath, backupCreated, err := backup.EnsureBackup(resolved.CanonicalPath, originalHash, originalBytes)
	if err != nil {
		return fail(err)
	}
	cleanupBackup := func() {
		if backupCreated {
			_ = backup.DeleteBackup(resolved.CanonicalPath, originalHash)
		}
	}

	if err := writeBinaryAtomically(resolved.CanonicalPath, originalHash, patchedBytes, resolved.Mode); err != nil {
		if repack.TargetMayHaveChanged(err) {
			return fail(preservedBackupError(err, backupPath, resolved.CanonicalPath))
		}
		if !repack.TargetMayHaveChanged(err) {
			cleanupBackup()
		}
		return fail(err)
	}

	if err := backup.SaveMetadata(backup.Metadata{
		CanonicalPath:   resolved.CanonicalPath,
		DisplayPath:     resolved.DisplayPath,
		DetectedVersion: inspection.Version,
		OriginalSHA256:  originalHash,
		PatchedSHA256:   patchedHash,
		IntervalMS:      *intervalMS,
		BackupPath:      backupPath,
		FileMode:        uint32(resolved.Mode.Perm()),
	}); err != nil {
		rollbackErr := writeBinaryAtomically(resolved.CanonicalPath, patchedHash, originalBytes, resolved.Mode)
		if rollbackErr != nil {
			return fail(fmt.Errorf("save metadata: %v; rollback failed: %w", err, rollbackErr))
		}
		cleanupBackup()
		return fail(fmt.Errorf("save metadata: %w", err))
	}

	fmt.Printf("patched: %s version=%s interval=%dms backup=%s\n", resolved.CanonicalPath, inspection.Version, *intervalMS, backupPath)
	return 0
}

func runCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	binaryPath := fs.String("binary", "", "path to the Claude binary (defaults to ~/.local/bin/claude)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	resolved, err := claude.Resolve(*binaryPath)
	if err != nil {
		return failCheck(err)
	}
	data, err := readBoundedFile(resolved.CanonicalPath, maxBinarySizeBytes)
	if err != nil {
		return failCheck(err)
	}
	currentHash := backup.SHA256Bytes(data)

	_, _, inspection, err := inspectBinary(data)
	if err != nil {
		return failCheck(err)
	}
	managed, err := backup.LoadMatchingRecord(resolved.CanonicalPath, currentHash)
	if err != nil {
		return failCheck(err)
	}

	fmt.Print(formatCheckOutput(resolved.CanonicalPath, inspection, managed != nil))

	switch inspection.State {
	case patch.StatePatched:
		return 0
	case patch.StateUnpatched:
		return 1
	case patch.StateUnrecognizedShape:
		return 2
	case patch.StateAmbiguousShape:
		return 4
	default:
		return 3
	}
}

func formatCheckOutput(binaryPath string, inspection patch.Inspection, managed bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "binary: %s\n", sanitizeOutputValue(binaryPath))
	fmt.Fprintf(&b, "version: %s\n", sanitizeOutputValue(inspection.Version))
	fmt.Fprintf(&b, "state: %s\n", inspection.State)
	if inspection.ShapeState == patch.ShapeStateKnown {
		fmt.Fprintf(&b, "shape_id: %s\n", inspection.ShapeID)
		fmt.Fprintf(&b, "observed_versions: %s\n", strings.Join(inspection.ObservedVersions, ", "))
	}
	fmt.Fprintf(&b, "shape_state: %s\n", inspection.ShapeState)
	fmt.Fprintf(&b, "patch_state: %s\n", inspection.PatchState)
	if inspection.State == patch.StatePatched {
		fmt.Fprintf(&b, "interval_ms: %d\n", inspection.IntervalMS)
	}
	support := supportClaim(inspection)
	fmt.Fprintf(&b, "support_claim: %s\n", support)
	fmt.Fprintf(&b, "verification_claim: %s\n", legacyVerificationClaim(support))
	fmt.Fprintf(&b, "quick_apply_candidate: %t\n", quickApplyCandidate(inspection))
	fmt.Fprintf(&b, "managed: %t\n", managed)
	return b.String()
}

func formatDryRunOutput(binaryPath string, current patch.Inspection, managed bool, intervalMS int, simulated *patch.Inspection, status, validation, reason string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "binary: %s\n", sanitizeOutputValue(binaryPath))
	fmt.Fprintf(&b, "version: %s\n", sanitizeOutputValue(current.Version))
	fmt.Fprintf(&b, "current_state: %s\n", current.State)
	if current.ShapeState == patch.ShapeStateKnown {
		fmt.Fprintf(&b, "shape_id: %s\n", current.ShapeID)
		fmt.Fprintf(&b, "observed_versions: %s\n", strings.Join(current.ObservedVersions, ", "))
	}
	fmt.Fprintf(&b, "current_shape_state: %s\n", current.ShapeState)
	fmt.Fprintf(&b, "current_patch_state: %s\n", current.PatchState)
	if current.State == patch.StatePatched {
		fmt.Fprintf(&b, "current_interval_ms: %d\n", current.IntervalMS)
	}
	support := supportClaim(current)
	fmt.Fprintf(&b, "support_claim: %s\n", support)
	fmt.Fprintf(&b, "verification_claim: %s\n", legacyVerificationClaim(support))
	fmt.Fprintf(&b, "quick_apply_candidate: %t\n", quickApplyCandidate(current))
	fmt.Fprintf(&b, "managed: %t\n", managed)
	fmt.Fprintf(&b, "dry_run: %s\n", status)
	if validation != "" {
		fmt.Fprintf(&b, "dry_run_rebuild_validation: %s\n", validation)
	}
	if reason != "" {
		fmt.Fprintf(&b, "dry_run_reason: %s\n", sanitizeOutputValue(reason))
	}
	if simulated != nil {
		fmt.Fprintf(&b, "simulated_state: %s\n", simulated.State)
		fmt.Fprintf(&b, "simulated_patch_state: %s\n", simulated.PatchState)
		if simulated.ShapeState == patch.ShapeStateKnown {
			fmt.Fprintf(&b, "simulated_shape_id: %s\n", simulated.ShapeID)
		}
		if simulated.State == patch.StatePatched {
			fmt.Fprintf(&b, "simulated_interval_ms: %d\n", simulated.IntervalMS)
		}
	}
	fmt.Fprintf(&b, "would_apply_interval_ms: %d\n", intervalMS)
	return b.String()
}

func legacyVerificationClaim(support string) string {
	if support == "live_verified" {
		return "live-verified"
	}
	return "not-live-verified"
}

func supportClaim(inspection patch.Inspection) string {
	if inspection.ShapeState == patch.ShapeStateKnown && inspection.PatchState != patch.PatchStateUnknown {
		if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" && patch.IsDocumentedLiveVerifiedVersion(inspection.Version) {
			return "live_verified"
		}
		return "patchable_only"
	}
	return "undocumented"
}

func quickApplyCandidate(inspection patch.Inspection) bool {
	return inspection.ShapeState == patch.ShapeStateKnown && inspection.PatchState == patch.PatchStateUnpatched
}

func runRestore(args []string) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	binaryPath := fs.String("binary", "", "path to the Claude binary (defaults to ~/.local/bin/claude)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	resolved, err := claude.Resolve(*binaryPath)
	if err != nil {
		return fail(err)
	}
	release, err := acquireEnsureLock(resolved.CanonicalPath)
	if err != nil {
		return fail(lockError(err))
	}
	defer func() {
		_ = release()
	}()
	currentBytes, err := readBoundedFile(resolved.CanonicalPath, maxBinarySizeBytes)
	if err != nil {
		return fail(err)
	}
	currentHash := backup.SHA256Bytes(currentBytes)

	managed, err := backup.LoadMatchingRecord(resolved.CanonicalPath, currentHash)
	if err != nil {
		return fail(err)
	}
	if managed == nil {
		if _, _, inspection, inspectErr := inspectBinary(currentBytes); inspectErr == nil && inspection.State == patch.StatePatched {
			return fail(errors.New("binary appears patched but is unmanaged; refusing restore"))
		}
		return fail(errors.New("no managed backup record found for this binary"))
	}

	if currentHash == managed.OriginalSHA256 {
		if err := cleanupManagedState(managed); err != nil {
			return fail(err)
		}
		fmt.Printf("already restored: %s\n", resolved.CanonicalPath)
		return 0
	}
	if currentHash != managed.PatchedSHA256 {
		return fail(errors.New("binary no longer matches the managed patched hash; refusing restore"))
	}

	backupPath, err := backup.ExpectedBackupPath(managed.CanonicalPath, managed.OriginalSHA256)
	if err != nil {
		return fail(err)
	}
	backupBytes, err := readBoundedFile(backupPath, maxBinarySizeBytes)
	if err != nil {
		return fail(err)
	}
	backupHash := backup.SHA256Bytes(backupBytes)
	if backupHash != managed.OriginalSHA256 {
		return fail(fmt.Errorf("managed backup hash mismatch: expected %s, found %s", managed.OriginalSHA256, backupHash))
	}

	mode := os.FileMode(managed.FileMode)
	if mode == 0 {
		mode = resolved.Mode
	}

	if err := writeBinaryAtomically(resolved.CanonicalPath, currentHash, backupBytes, mode); err != nil {
		return fail(err)
	}
	if err := cleanupManagedState(managed); err != nil {
		return fail(err)
	}

	fmt.Printf("restored: %s from %s\n", resolved.CanonicalPath, backupPath)
	return 0
}

func lockError(err error) error {
	if errors.Is(err, targetlock.ErrBusy) {
		return errors.New("target binary is busy; another patch transaction holds the per-binary lock")
	}
	return fmt.Errorf("acquire target lock: %w", err)
}

func runApplyDryRun(canonicalPath, originalHash string, originalBytes []byte, bundle *bun.Bundle, graph *bun.ModuleGraph, inspection patch.Inspection, managed *backup.Metadata, intervalMS int) int {
	managedMatch := managed != nil && managed.PatchedSHA256 == originalHash

	switch inspection.State {
	case patch.StatePatched:
		if !managedMatch {
			fmt.Print(formatDryRunOutput(canonicalPath, inspection, false, intervalMS, nil, "blocked", "", "already_patched_unmanaged"))
			return 1
		}
		if inspection.IntervalMS == intervalMS {
			fmt.Print(formatDryRunOutput(canonicalPath, inspection, true, intervalMS, &inspection, "ok", "skipped_already_patched", "already_patched_same_interval"))
			return 0
		}
		fmt.Print(formatDryRunOutput(canonicalPath, inspection, true, intervalMS, nil, "blocked", "", fmt.Sprintf("restore_required_for_interval_change current_interval_ms=%d", inspection.IntervalMS)))
		return 1
	case patch.StateAmbiguousShape:
		fmt.Print(formatDryRunOutput(canonicalPath, inspection, managed != nil, intervalMS, nil, "blocked", "", "ambiguous_shape"))
		return 1
	case patch.StateUnrecognizedShape:
		fmt.Print(formatDryRunOutput(canonicalPath, inspection, managed != nil, intervalMS, nil, "blocked", "", "unrecognized_shape"))
		return 1
	case patch.StateUnpatched:
		patchedBytes, postInspection, err := rebuildPatchedBinary(originalBytes, bundle, graph, inspection, intervalMS)
		if err != nil {
			return fail(err)
		}
		_ = patchedBytes
		fmt.Print(formatDryRunOutput(canonicalPath, inspection, managed != nil, intervalMS, &postInspection, "ok", "passed", ""))
		return 0
	default:
		fmt.Print(formatDryRunOutput(canonicalPath, inspection, managed != nil, intervalMS, nil, "blocked", "", fmt.Sprintf("unknown_state %s", inspection.State)))
		return 1
	}
}

func rebuildPatchedBinary(originalBytes []byte, bundle *bun.Bundle, graph *bun.ModuleGraph, inspection patch.Inspection, intervalMS int) ([]byte, patch.Inspection, error) {
	entryIndex, entryModule, err := graph.EntryPointModule()
	if err != nil {
		return nil, patch.Inspection{}, err
	}
	entryContents, err := graph.Slice(entryModule.Contents)
	if err != nil {
		return nil, patch.Inspection{}, err
	}
	patchedContents, err := patch.ApplyInspection(entryContents, inspection, intervalMS)
	if err != nil {
		return nil, patch.Inspection{}, err
	}
	patchedPayload, err := graph.ReplaceModuleContents(entryIndex, patchedContents)
	if err != nil {
		return nil, patch.Inspection{}, err
	}
	patchedBytes, err := bun.ReplacePayload(originalBytes, bundle.Metadata, patchedPayload)
	if err != nil {
		return nil, patch.Inspection{}, fmt.Errorf("replace embedded payload: %w", err)
	}
	_, _, postInspection, err := inspectBinary(patchedBytes)
	if err != nil {
		return nil, patch.Inspection{}, fmt.Errorf("re-validate rebuilt binary: %w", err)
	}
	if postInspection.State != patch.StatePatched || postInspection.IntervalMS != intervalMS {
		return nil, patch.Inspection{}, fmt.Errorf("re-validate rebuilt binary: expected patched %dms, got %s %dms", intervalMS, postInspection.State, postInspection.IntervalMS)
	}
	return patchedBytes, postInspection, nil
}

func inspectBinary(data []byte) (*bun.Bundle, *bun.ModuleGraph, patch.Inspection, error) {
	bundle, err := bun.Extract(data)
	if err != nil {
		return nil, nil, patch.Inspection{}, fmt.Errorf("extract embedded Bun payload: %w", err)
	}
	graph, err := bun.ParseModuleGraph(bundle.Metadata.Format, bundle.Payload)
	if err != nil {
		return nil, nil, patch.Inspection{}, fmt.Errorf("parse embedded Bun module graph: %w", err)
	}
	_, entryModule, err := graph.EntryPointModule()
	if err != nil {
		return nil, nil, patch.Inspection{}, err
	}
	entryContents, err := graph.Slice(entryModule.Contents)
	if err != nil {
		return nil, nil, patch.Inspection{}, err
	}
	return bundle, graph, patch.Inspect(entryContents), nil
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, err)
	return 1
}

func failCheck(err error) int {
	fmt.Fprintln(os.Stderr, err)
	return 3
}

func readBoundedFile(path string, maxSize int64) ([]byte, error) {
	return fileutil.ReadBoundedRegularFile(path, "target binary", maxSize)
}

func printUsage(w *os.File) {
	fmt.Fprintf(w, "usage: %s {ensure|apply|check|restore|version} [flags]\n", filepath.Base(os.Args[0]))
}

func sanitizeOutputValue(value string) string {
	return strings.NewReplacer(
		"\r", `\r`,
		"\n", `\n`,
	).Replace(value)
}
