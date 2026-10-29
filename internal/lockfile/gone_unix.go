//go:build unix

package lockfile

import (
	"errors"
	"syscall"
)

// processGone reports that no process with pid exists on this machine. A
// pid that exists — whoever it is now — is not gone: a reused pid keeps a
// lock until it is stale, which is the safe mistake.
func processGone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
