package backup

import (
	"os"
	"path/filepath"
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
