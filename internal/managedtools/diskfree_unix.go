//go:build linux || darwin || freebsd || netbsd || openbsd

package managedtools

import "syscall"

// freeBytes is how many bytes this user may write at dir, or -1.
func freeBytes(dir string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return -1
	}
	return int64(stat.Bavail) * int64(stat.Bsize)
}
