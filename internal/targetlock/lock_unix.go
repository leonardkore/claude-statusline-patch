//go:build !windows

package targetlock

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

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
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open ensure lock %s: %w", lockPath, err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if err == unix.EWOULDBLOCK {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("acquire ensure lock %s: %w", lockPath, err)
	}

	return func() error {
		unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
		closeErr := file.Close()
		if unlockErr != nil {
			return fmt.Errorf("unlock ensure lock %s: %w", lockPath, unlockErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close ensure lock %s: %w", lockPath, closeErr)
		}
		return nil
	}, nil
}
