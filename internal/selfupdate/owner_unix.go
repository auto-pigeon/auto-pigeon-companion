//go:build unix

package selfupdate

import (
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether a file belongs to this process's user.
func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return !ok || int(stat.Uid) == os.Getuid()
}
