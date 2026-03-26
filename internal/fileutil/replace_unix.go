//go:build !windows

package fileutil

import "os"

func ReplaceFile(fromPath, toPath string) error {
	return os.Rename(fromPath, toPath)
}
