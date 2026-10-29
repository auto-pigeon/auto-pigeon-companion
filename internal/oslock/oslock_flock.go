//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package oslock

import (
	"errors"
	"os"
	"syscall"
)

// TryLock takes the operating system's exclusive lock on file without
// waiting. The kernel lets go of it when the holder's process ends.
func TryLock(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EINTR) {
		return false, nil
	}
	return err == nil, err
}

func Unlock(file *os.File) error { return syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }
