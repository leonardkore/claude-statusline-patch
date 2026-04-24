//go:build windows

package targetlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"

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
	overlapped := &windows.Overlapped{}
	lockErr := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		overlapped,
	)
	if lockErr != nil {
		_ = file.Close()
		if errors.Is(lockErr, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("acquire ensure lock %s: %w", lockPath, lockErr)
	}
	return func() error {
		unlockErr := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
		closeErr := file.Close()
		removeErr := os.Remove(lockPath)
		if unlockErr != nil {
			return fmt.Errorf("unlock ensure lock %s: %w", lockPath, unlockErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close ensure lock %s: %w", lockPath, closeErr)
		}
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("remove ensure lock %s: %w", lockPath, removeErr)
		}
		return nil
	}, nil
}
