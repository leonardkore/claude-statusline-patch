package repack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/leonardkore/claude-statusline-patch/internal/backup"
	"github.com/leonardkore/claude-statusline-patch/internal/fileutil"
)

type writeStage string

const (
	writeStagePreCommit  writeStage = "pre_commit"
	writeStagePostCommit writeStage = "post_commit"
)

type AtomicWriteError struct {
	stage writeStage
	err   error
}

func (e *AtomicWriteError) Error() string {
	return fmt.Sprintf("%s atomic write failure: %v", e.stage, e.err)
}

func (e *AtomicWriteError) Unwrap() error {
	return e.err
}

func (e *AtomicWriteError) Stage() string {
	return string(e.stage)
}

func TargetMayHaveChanged(err error) bool {
	var atomicErr *AtomicWriteError
	return errors.As(err, &atomicErr) && atomicErr.stage == writeStagePostCommit
}

func NewPostCommitError(err error) error {
	return &AtomicWriteError{stage: writeStagePostCommit, err: err}
}

var (
	sha256File  = backup.SHA256File
	replaceFile = fileutil.ReplaceFile
)

func WriteAtomically(targetPath, expectedCurrentHash string, data []byte, mode os.FileMode) error {
	currentHash, err := sha256File(targetPath)
	if err != nil {
		return &AtomicWriteError{stage: writeStagePreCommit, err: fmt.Errorf("hash current target: %w", err)}
	}
	if currentHash != expectedCurrentHash {
		return &AtomicWriteError{stage: writeStagePreCommit, err: fmt.Errorf("target changed during patch transaction: expected %s, found %s", expectedCurrentHash, currentHash)}
	}

	dir := filepath.Dir(targetPath)
	temp, err := os.CreateTemp(dir, filepath.Base(targetPath)+".tmp-*")
	if err != nil {
		return &AtomicWriteError{stage: writeStagePreCommit, err: fmt.Errorf("create temp file: %w", err)}
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
		return &AtomicWriteError{stage: writeStagePreCommit, err: fmt.Errorf("write temp file: %w", err)}
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return &AtomicWriteError{stage: writeStagePreCommit, err: fmt.Errorf("chmod temp file: %w", err)}
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return &AtomicWriteError{stage: writeStagePreCommit, err: fmt.Errorf("sync temp file: %w", err)}
	}
	if err := temp.Close(); err != nil {
		return &AtomicWriteError{stage: writeStagePreCommit, err: fmt.Errorf("close temp file: %w", err)}
	}

	if err := syncDir(dir); err != nil {
		return &AtomicWriteError{stage: writeStagePreCommit, err: err}
	}
	currentHash, err = sha256File(targetPath)
	if err != nil {
		return &AtomicWriteError{stage: writeStagePreCommit, err: fmt.Errorf("re-hash current target before swap: %w", err)}
	}
	if currentHash != expectedCurrentHash {
		return &AtomicWriteError{stage: writeStagePreCommit, err: fmt.Errorf("target changed during patch transaction before final swap: expected %s, found %s", expectedCurrentHash, currentHash)}
	}
	if err := replaceFile(tempName, targetPath); err != nil {
		return &AtomicWriteError{stage: writeStagePreCommit, err: fmt.Errorf("rename temp file: %w", err)}
	}
	committed = true
	if err := syncDir(dir); err != nil {
		return &AtomicWriteError{stage: writeStagePostCommit, err: err}
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
