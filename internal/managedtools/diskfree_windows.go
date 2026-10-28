//go:build windows

package managedtools

import (
	"syscall"
	"unsafe"
)

var getDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// freeBytes is how many bytes this user may write at dir, or -1.
func freeBytes(dir string) int64 {
	path, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return -1
	}
	var available, total, free uint64
	ok, _, _ := getDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(path)),
		uintptr(unsafe.Pointer(&available)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free)))
	if ok == 0 {
		return -1
	}
	return int64(available)
}
