//go:build windows

package fsshare

import (
	"errors"
	"syscall"
)

// The Win32 errors a file in use by another handle answers with. The standard
// library names ERROR_ACCESS_DENIED; the other two are their documented values.
const (
	errorSharingViolation syscall.Errno = 32
	errorLockViolation    syscall.Errno = 33
)

func isBusy(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == errorSharingViolation || errno == errorLockViolation || errno == syscall.ERROR_ACCESS_DENIED
}
