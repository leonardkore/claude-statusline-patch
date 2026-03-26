package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRejectsDirectoriesAndNonRegularFiles(t *testing.T) {
	dir := t.TempDir()

	if _, err := Resolve(dir); err == nil {
		t.Fatalf("expected directory target to be rejected")
	}

	fifoPath := filepath.Join(dir, "fifo")
	if err := os.WriteFile(fifoPath, []byte("fake binary"), 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	resolved, err := Resolve(fifoPath)
	if err != nil {
		t.Fatalf("Resolve failed for regular file fixture: %v", err)
	}
	if resolved.Size != int64(len("fake binary")) {
		t.Fatalf("expected recorded size %d, got %d", len("fake binary"), resolved.Size)
	}
}

func TestResolveUsesVersionFromCanonicalPath(t *testing.T) {
	root := t.TempDir()
	versionDir := filepath.Join(root, "versions", "2.1.84")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	canonicalPath := filepath.Join(versionDir, "claude")
	if err := os.WriteFile(canonicalPath, []byte("fake binary"), 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	linkPath := filepath.Join(root, "claude-link")
	if err := os.Symlink(canonicalPath, linkPath); err != nil {
		t.Fatalf("Symlink failed: %v", err)
	}

	resolved, err := Resolve(linkPath)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if resolved.CanonicalPath != canonicalPath {
		t.Fatalf("expected canonical path %s, got %s", canonicalPath, resolved.CanonicalPath)
	}
	if resolved.Version != "2.1.84" {
		t.Fatalf("expected version 2.1.84, got %s", resolved.Version)
	}
}
