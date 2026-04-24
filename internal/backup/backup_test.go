package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateRootRejectsRelativeXDGStateHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "relative-state")

	if _, err := StateRoot(); err == nil {
		t.Fatalf("expected relative XDG_STATE_HOME to be rejected")
	}
}

func TestStateRootRejectsOutsideHome(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", outside)

	if _, err := StateRoot(); err == nil {
		t.Fatalf("expected outside-home XDG_STATE_HOME to be rejected")
	}
}

func TestLoadMatchingRecordSkipsTamperedMetadataAndReturnsLatestMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	canonicalPath := filepath.Join(home, "bin", "claude")
	targetDir, err := TargetDir(canonicalPath)
	if err != nil {
		t.Fatalf("TargetDir failed: %v", err)
	}
	if err := os.MkdirAll(targetDir, stateDirMode); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	first := Metadata{
		CanonicalPath:   canonicalPath,
		DisplayPath:     canonicalPath,
		DetectedVersion: "2.1.84",
		OriginalSHA256:  "orig",
		PatchedSHA256:   "patched-old",
		IntervalMS:      1000,
		FileMode:        0o755,
	}
	if err := SaveMetadata(first); err != nil {
		t.Fatalf("SaveMetadata first failed: %v", err)
	}

	invalidPath, err := MetadataPath(canonicalPath, "invalid")
	if err != nil {
		t.Fatalf("MetadataPath failed: %v", err)
	}
	invalid := `{"schema_version":1,"canonical_path":"` + canonicalPath + `","path_key":"` + PathKey(canonicalPath) + `","original_sha256":"orig","patched_sha256":"patched-now","backup_path":"` + filepath.Join(home, "outside.bin") + `"}`
	if err := os.WriteFile(invalidPath, []byte(invalid), metadataMode); err != nil {
		t.Fatalf("WriteFile invalid metadata failed: %v", err)
	}

	second := first
	second.PatchedSHA256 = "patched-now"
	second.IntervalMS = 1500
	if err := SaveMetadata(second); err != nil {
		t.Fatalf("SaveMetadata second failed: %v", err)
	}

	record, err := LoadMatchingRecord(canonicalPath, "patched-now")
	if err != nil {
		t.Fatalf("LoadMatchingRecord failed: %v", err)
	}
	if record == nil {
		t.Fatalf("expected matching metadata record")
	}
	if record.IntervalMS != 1500 {
		t.Fatalf("expected latest matching record, got interval %d", record.IntervalMS)
	}
	expectedBackupPath, err := ExpectedBackupPath(canonicalPath, first.OriginalSHA256)
	if err != nil {
		t.Fatalf("ExpectedBackupPath failed: %v", err)
	}
	if record.BackupPath != expectedBackupPath {
		t.Fatalf("expected backup path %s, got %s", expectedBackupPath, record.BackupPath)
	}
}

func TestDeleteMetadataRemovesSavedRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	canonicalPath := filepath.Join(home, "bin", "claude")
	meta := Metadata{
		CanonicalPath:   canonicalPath,
		DisplayPath:     canonicalPath,
		DetectedVersion: "2.1.84",
		OriginalSHA256:  "orig",
		PatchedSHA256:   "patched",
		IntervalMS:      1000,
		FileMode:        0o755,
	}
	if err := SaveMetadata(meta); err != nil {
		t.Fatalf("SaveMetadata failed: %v", err)
	}

	if err := DeleteMetadata(canonicalPath, "orig"); err != nil {
		t.Fatalf("DeleteMetadata failed: %v", err)
	}

	record, err := LoadMatchingRecord(canonicalPath, "patched")
	if err != nil {
		t.Fatalf("LoadMatchingRecord failed: %v", err)
	}
	if record != nil {
		t.Fatalf("expected metadata to be deleted")
	}
}

func TestEnsureBackupReportsWhetherItCreatedTheBackup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	canonicalPath := filepath.Join(home, "bin", "claude")
	contents := []byte("original-binary")
	originalHash := SHA256Bytes(contents)

	path, created, err := EnsureBackup(canonicalPath, originalHash, contents)
	if err != nil {
		t.Fatalf("EnsureBackup first call failed: %v", err)
	}
	if !created {
		t.Fatalf("expected first EnsureBackup call to create the backup")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected backup file to exist: %v", err)
	}

	pathAgain, createdAgain, err := EnsureBackup(canonicalPath, originalHash, contents)
	if err != nil {
		t.Fatalf("EnsureBackup second call failed: %v", err)
	}
	if createdAgain {
		t.Fatalf("expected second EnsureBackup call to reuse the existing backup")
	}
	if pathAgain != path {
		t.Fatalf("expected backup path %s, got %s", path, pathAgain)
	}

	if err := DeleteBackup(canonicalPath, originalHash); err != nil {
		t.Fatalf("DeleteBackup failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected backup to be removed, got err=%v", err)
	}
}

func TestEnsureBackupRejectsExistingBackupHashMismatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	canonicalPath := filepath.Join(home, "bin", "claude")
	contents := []byte("original-binary")
	originalHash := SHA256Bytes(contents)

	backupPath, err := ExpectedBackupPath(canonicalPath, originalHash)
	if err != nil {
		t.Fatalf("ExpectedBackupPath failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(backupPath), stateDirMode); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(backupPath, []byte("tampered-binary"), backupFileMode); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	_, created, err := EnsureBackup(canonicalPath, originalHash, contents)
	if err == nil {
		t.Fatalf("expected existing backup hash mismatch")
	}
	if created {
		t.Fatalf("expected mismatched backup not to be reported as newly created")
	}
	if got := filepath.Base(backupPath); !strings.Contains(err.Error(), got) {
		t.Fatalf("expected error to mention backup path %s, got %v", got, err)
	}
}

func TestLoadVerifiedOutcomeRequiresExactTupleMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	canonicalPath := filepath.Join(home, "bin", "claude")
	record := VerifiedOutcome{
		CanonicalPath:           canonicalPath,
		InstalledSHA256:         "patched-sha",
		IntervalMS:              1000,
		PlatformGOOS:            "linux",
		PlatformGOARCH:          "amd64",
		VerifierContractVersion: 1,
		DetectedVersion:         "2.1.97",
		VerifierRunID:           "run-1",
		EventsFile:              "events.jsonl",
		PaneCaptureFile:         "pane.txt",
		DistinctSessionSeconds:  []int{0, 1, 2, 3, 4},
	}
	if err := SaveVerifiedOutcome(record); err != nil {
		t.Fatalf("SaveVerifiedOutcome failed: %v", err)
	}

	exact, err := LoadVerifiedOutcome(canonicalPath, "patched-sha", 1000, "linux", "amd64", 1)
	if err != nil {
		t.Fatalf("LoadVerifiedOutcome exact failed: %v", err)
	}
	if exact == nil {
		t.Fatalf("expected exact verified outcome match")
	}

	mismatchCases := []struct {
		name            string
		installedSHA    string
		intervalMS      int
		goos            string
		goarch          string
		contractVersion int
	}{
		{name: "hash", installedSHA: "other-sha", intervalMS: 1000, goos: "linux", goarch: "amd64", contractVersion: 1},
		{name: "interval", installedSHA: "patched-sha", intervalMS: 1500, goos: "linux", goarch: "amd64", contractVersion: 1},
		{name: "platform", installedSHA: "patched-sha", intervalMS: 1000, goos: "darwin", goarch: "amd64", contractVersion: 1},
		{name: "contract", installedSHA: "patched-sha", intervalMS: 1000, goos: "linux", goarch: "amd64", contractVersion: 2},
	}
	for _, tc := range mismatchCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := LoadVerifiedOutcome(canonicalPath, tc.installedSHA, tc.intervalMS, tc.goos, tc.goarch, tc.contractVersion)
			if err != nil {
				t.Fatalf("LoadVerifiedOutcome mismatch failed: %v", err)
			}
			if got != nil {
				t.Fatalf("expected no verified outcome for mismatch case %s", tc.name)
			}
		})
	}
}

func TestDeleteVerifiedOutcomesRemovesAllVerifiedRecords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	canonicalPath := filepath.Join(home, "bin", "claude")
	base := VerifiedOutcome{
		CanonicalPath:           canonicalPath,
		InstalledSHA256:         "patched-sha",
		IntervalMS:              1000,
		PlatformGOOS:            "linux",
		PlatformGOARCH:          "amd64",
		VerifierContractVersion: 1,
		DetectedVersion:         "2.1.97",
		VerifierRunID:           "run-1",
		EventsFile:              "events.jsonl",
		PaneCaptureFile:         "pane.txt",
		DistinctSessionSeconds:  []int{0, 1, 2, 3, 4},
	}
	if err := SaveVerifiedOutcome(base); err != nil {
		t.Fatalf("SaveVerifiedOutcome failed: %v", err)
	}
	base.InstalledSHA256 = "other-patched-sha"
	base.VerifierRunID = "run-2"
	if err := SaveVerifiedOutcome(base); err != nil {
		t.Fatalf("SaveVerifiedOutcome second failed: %v", err)
	}
	if err := DeleteVerifiedOutcomes(canonicalPath); err != nil {
		t.Fatalf("DeleteVerifiedOutcomes failed: %v", err)
	}
	records, err := LoadAllVerifiedOutcomes(canonicalPath)
	if err != nil {
		t.Fatalf("LoadAllVerifiedOutcomes failed: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected no verified outcomes after delete, got %d", len(records))
	}
}
