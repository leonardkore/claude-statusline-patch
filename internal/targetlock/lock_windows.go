//go:build windows

package targetlock

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/leonardkore/claude-statusline-patch/internal/backup"
)

type ReleaseFunc func() error

func Acquire(canonicalPath string) (ReleaseFunc, error) {
	targetDir, err := backup.TargetDir(canonicalPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return nil, fmt.Errorf("create target lock dir: %w", err)
	}
	lockPath := filepath.Join(targetDir, "ensure.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("create ensure lock %s: %w", lockPath, err)
	}
	return func() error {
		closeErr := file.Close()
		removeErr := os.Remove(lockPath)
		if closeErr != nil {
			return fmt.Errorf("close ensure lock %s: %w", lockPath, closeErr)
		}
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("remove ensure lock %s: %w", lockPath, removeErr)
		}
		return nil
	}, nil
}
