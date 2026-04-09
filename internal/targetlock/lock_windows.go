//go:build windows

package targetlock

type ReleaseFunc func() error

func Acquire(canonicalPath string) (ReleaseFunc, error) {
	return func() error { return nil }, nil
}
