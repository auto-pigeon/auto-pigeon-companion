//go:build windows

package tooluse

import (
	"os"
	"syscall"
	"unsafe"
)

// LockFileEx is not in the standard library's syscall; this program has no
// dependencies, so it is called from kernel32 as other Windows calls here are.
var (
	procLockFileEx   = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
	procUnlockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")
)

const (
	lockfileFailImmediately = 0x1
	lockfileExclusiveLock   = 0x2
	errorLockViolation      = syscall.Errno(33)
)

// tryLock takes the operating system's exclusive lock on file without
// waiting. Windows lets go of it when the holder's process ends.
func tryLock(file *os.File) (bool, error) {
	var overlapped syscall.Overlapped
	ok, _, err := procLockFileEx.Call(file.Fd(), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0,
		uintptr(unsafe.Pointer(&overlapped)))
	if ok != 0 {
		return true, nil
	}
	if err == errorLockViolation || err == syscall.ERROR_IO_PENDING {
		return false, nil
	}
	return false, err
}

func unlockFile(file *os.File) error {
	var overlapped syscall.Overlapped
	ok, _, err := procUnlockFileEx.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if ok == 0 {
		return err
	}
	return nil
}
