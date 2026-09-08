//go:build !windows

package job

import (
	"os/signal"
	"syscall"
)

// ignoreTermination makes the helper deaf to the polite signal.
//
// The executor's contract is SIGTERM, a grace period, then SIGKILL. Every
// cancellation fixture before `AUCOM/AUT 229` used the `sleep` mode, which dies
// on the first signal — so the grace period elapsing and the forceful pass
// actually working were the two halves of [execution.supervise] that no test
// had ever run. A tool that traps SIGTERM to finish writing a file is normal;
// one that traps it and never leaves is what the second pass is for.
func ignoreTermination() { signal.Ignore(syscall.SIGTERM) }

// processAlive reports whether one process still exists, by pid.
//
// [processGroupAlive] asks about a process GROUP, which is the right question
// for the job's leader and the wrong one for the children it started: they are
// in the leader's group, so `kill(-childpid, 0)` asks about a group that does
// not exist. Signal 0 delivers nothing and performs the existence check.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
