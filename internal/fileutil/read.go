package fileutil

import (
	"fmt"
	"os"
)

func ReadBoundedRegularFile(path, label string, maxSize int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s %s: %w", label, path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file: %s", label, path)
	}
	if info.Size() < 0 || info.Size() > maxSize {
		return nil, fmt.Errorf("refusing to read %s %s: size %d exceeds limit %d", label, path, info.Size(), maxSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	return data, nil
}
