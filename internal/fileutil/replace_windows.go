//go:build windows

package fileutil

import "golang.org/x/sys/windows"

func ReplaceFile(fromPath, toPath string) error {
	fromUTF16, err := windows.UTF16PtrFromString(fromPath)
	if err != nil {
		return err
	}
	toUTF16, err := windows.UTF16PtrFromString(toPath)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		fromUTF16,
		toUTF16,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}
