package repack

import (
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
