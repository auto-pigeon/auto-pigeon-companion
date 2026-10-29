//go:build windows

package selfupdate

import (
	"os"
	"syscall"
	"unsafe"
)

var procMoveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
)

// renameDurable is MoveFileExW with MOVEFILE_WRITE_THROUGH: it returns once
// the move is flushed. A volume that refuses write-through falls back to
// os.Rename. Not yet observed on native Windows (01_NEW_368 carries the row
// to prompt 02).
func renameDurable(from, to string) error {
	source, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	target, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	if ok, _, callErr := procMoveFileExW.Call(uintptr(unsafe.Pointer(source)), uintptr(unsafe.Pointer(target)),
		moveFileReplaceExisting|moveFileWriteThrough); ok == 0 {
		// ERROR_INVALID_PARAMETER, ERROR_NOT_SUPPORTED
		if callErr == syscall.Errno(87) || callErr == syscall.Errno(50) {
			return os.Rename(from, to)
		}
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: callErr}
	}
	return nil
}

// syncDir: Windows has no directory fsync; the write-through rename is what
// flushes the entry.
func syncDir(string) {}
