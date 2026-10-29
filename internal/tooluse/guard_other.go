//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package tooluse

import (
	"errors"
	"os"
)

// No operating-system lock is wired up here: a gate cannot be changed, and
// a switch is refused rather than made unsafely.
func tryLock(*os.File) (bool, error) { return false, errors.ErrUnsupported }

func unlockFile(*os.File) error { return nil }
