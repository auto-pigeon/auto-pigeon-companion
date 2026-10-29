//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package oslock

import (
	"errors"
	"os"
)

// No operating-system lock is wired up here. A caller refuses what it would
// have done under the lock rather than do it unsafely.
func TryLock(*os.File) (bool, error) { return false, errors.ErrUnsupported }

func Unlock(*os.File) error { return nil }
