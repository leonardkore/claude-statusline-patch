package repack

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonardkore/claude-statusline-patch/internal/backup"
)

func TestWriteAtomicallyReplacesFileWhenHashMatches(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "claude")
	original := []byte("original")
	replacement := []byte("replacement")
	if err := os.WriteFile(targetPath, original, 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := WriteAtomically(targetPath, backup.SHA256Bytes(original), replacement, 0o755); err != nil {
		t.Fatalf("WriteAtomically failed: %v", err)
	}

	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(got) != string(replacement) {
		t.Fatalf("expected %q, got %q", replacement, got)
	}
}

func TestWriteAtomicallyRejectsHashMismatch(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "claude")
	if err := os.WriteFile(targetPath, []byte("original"), 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := WriteAtomically(targetPath, backup.SHA256Bytes([]byte("different")), []byte("replacement"), 0o755); err == nil {
		t.Fatalf("expected hash mismatch")
	}
}

func TestWriteAtomicallyRevalidatesTargetBeforeSwap(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "claude")
	original := []byte("original")
	replacement := []byte("replacement")
	if err := os.WriteFile(targetPath, original, 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	originalSHA256File := sha256File
	originalReplaceFile := replaceFile
	t.Cleanup(func() {
		sha256File = originalSHA256File
		replaceFile = originalReplaceFile
	})

	callCount := 0
	sha256File = func(path string) (string, error) {
		callCount++
		if callCount == 1 {
			return backup.SHA256Bytes(original), nil
		}
		return backup.SHA256Bytes([]byte("newer-binary")), nil
	}

	replaceCalled := false
	replaceFile = func(fromPath, toPath string) error {
		replaceCalled = true
		return originalReplaceFile(fromPath, toPath)
	}

	err := WriteAtomically(targetPath, backup.SHA256Bytes(original), replacement, 0o755)
	if err == nil {
		t.Fatalf("expected revalidation mismatch")
	}
	if replaceCalled {
		t.Fatalf("expected replace not to run when final hash revalidation fails")
	}
	if TargetMayHaveChanged(err) {
		t.Fatalf("expected pre-commit mismatch not to mark target as changed")
	}
	got, readErr := os.ReadFile(targetPath)
	if readErr != nil {
		t.Fatalf("ReadFile failed: %v", readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("expected original target contents to remain, got %q", got)
	}
}

func TestTargetMayHaveChangedRecognizesPostCommitErrors(t *testing.T) {
	err := &AtomicWriteError{
		stage: writeStagePostCommit,
		err:   errors.New("sync directory: boom"),
	}
	if !TargetMayHaveChanged(err) {
		t.Fatalf("expected post-commit error to report changed target")
	}
}
