package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/leonardkore/claude-statusline-patch/internal/backup"
	"github.com/leonardkore/claude-statusline-patch/internal/bun"
	"github.com/leonardkore/claude-statusline-patch/internal/claude"
	"github.com/leonardkore/claude-statusline-patch/internal/patch"
	"github.com/leonardkore/claude-statusline-patch/internal/repack"
	"github.com/leonardkore/claude-statusline-patch/internal/version"
)

const maxBinarySizeBytes int64 = 1 << 30

func Main(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return 2
	}

	switch args[0] {
	case "apply":
		return runApply(args[1:])
	case "check":
		return runCheck(args[1:])
	case "restore":
		return runRestore(args[1:])
	case "version":
		fmt.Println(version.Version)
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
	case patch.StateAmbiguous:
		return fail(errors.New("refusing to patch: statusline match is ambiguous"))
	case patch.StateUnsupported:
		return fail(fmt.Errorf("refusing to patch unsupported binary version %q; only Claude %s is supported", inspection.Version, patch.SupportedVersion))
	case patch.StateUnpatched:
		// continue
	default:
		return fail(fmt.Errorf("refusing to patch unknown state %q", inspection.State))
	}

	if inspection.Version != patch.SupportedVersion {
		return fail(fmt.Errorf("refusing to patch Claude version %q; only %s is supported", inspection.Version, patch.SupportedVersion))
	}

	backupPath, err := backup.EnsureBackup(resolved.CanonicalPath, originalHash, originalBytes)
	if err != nil {
		return fail(err)
	}

	entryIndex, entryModule, err := graph.EntryPointModule()
	if err != nil {
		return fail(err)
	}
	entryContents, err := graph.Slice(entryModule.Contents)
	if err != nil {
		return fail(err)
	}
	patchedContents, err := patch.ApplyKnownUnpatched(entryContents, *intervalMS)
	if err != nil {
		return fail(err)
	}
	patchedPayload, err := graph.ReplaceModuleContents(entryIndex, patchedContents)
	if err != nil {
		return fail(err)
	}
	patchedBytes, err := bun.ReplacePayload(originalBytes, bundle.Metadata, patchedPayload)
	if err != nil {
		return fail(fmt.Errorf("replace embedded payload: %w", err))
	}
	patchedHash := backup.SHA256Bytes(patchedBytes)

	if _, _, postInspection, err := inspectBinary(patchedBytes); err != nil {
		return fail(fmt.Errorf("re-validate rebuilt binary: %w", err))
	} else if postInspection.State != patch.StatePatched || postInspection.IntervalMS != *intervalMS {
		return fail(fmt.Errorf("re-validate rebuilt binary: expected patched %dms, got %s %dms", *intervalMS, postInspection.State, postInspection.IntervalMS))
	}

	if err := repack.WriteAtomically(resolved.CanonicalPath, originalHash, patchedBytes, resolved.Mode); err != nil {
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
		rollbackErr := repack.WriteAtomically(resolved.CanonicalPath, patchedHash, originalBytes, resolved.Mode)
		if rollbackErr != nil {
			return fail(fmt.Errorf("save metadata: %v; rollback failed: %w", err, rollbackErr))
		}
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

	fmt.Printf("binary: %s\n", resolved.CanonicalPath)
	fmt.Printf("version: %s\n", inspection.Version)
	fmt.Printf("state: %s\n", inspection.State)
	if inspection.State == patch.StatePatched {
		fmt.Printf("interval_ms: %d\n", inspection.IntervalMS)
	}
	fmt.Printf("managed: %t\n", managed != nil)

	switch inspection.State {
	case patch.StatePatched:
		return 0
	case patch.StateUnpatched:
		return 1
	case patch.StateUnsupported:
		return 2
	case patch.StateAmbiguous:
		return 4
	default:
		return 3
	}
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

	if err := repack.WriteAtomically(resolved.CanonicalPath, currentHash, backupBytes, mode); err != nil {
		return fail(err)
	}
	if err := backup.DeleteMetadata(resolved.CanonicalPath, managed.OriginalSHA256); err != nil {
		return fail(err)
	}

	fmt.Printf("restored: %s from %s\n", resolved.CanonicalPath, backupPath)
	return 0
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
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("target is not a regular file: %s", path)
	}
	if info.Size() < 0 || info.Size() > maxSize {
		return nil, fmt.Errorf("refusing to read %s: size %d exceeds limit %d", path, info.Size(), maxSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read target binary: %w", err)
	}
	return data, nil
}

func printUsage(w *os.File) {
	fmt.Fprintf(w, "usage: %s {apply|check|restore|version} [flags]\n", filepath.Base(os.Args[0]))
}
