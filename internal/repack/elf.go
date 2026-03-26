package repack

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/leonardkore/claude-statusline-patch/internal/backup"
)

func WriteAtomically(targetPath, expectedCurrentHash string, data []byte, mode os.FileMode) error {
	currentHash, err := backup.SHA256File(targetPath)
	if err != nil {
		return fmt.Errorf("hash current target: %w", err)
	}
	if currentHash != expectedCurrentHash {
		return fmt.Errorf("target changed during patch transaction: expected %s, found %s", expectedCurrentHash, currentHash)
	}

	dir := filepath.Dir(targetPath)
	temp, err := os.CreateTemp(dir, filepath.Base(targetPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tempName := temp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tempName)
		}
	}()

	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := syncDir(dir); err != nil {
		return err
	}
	if err := os.Rename(tempName, targetPath); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	committed = true
	if err := syncDir(dir); err != nil {
		return err
	}
	return nil
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}
