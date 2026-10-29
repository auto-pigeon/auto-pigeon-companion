package job

import (
	"errors"
	"io/fs"
)

// ProcessStartTicks is when the kernel started pid, zero where that cannot be
// read on this platform. With the pid it is a [ProcessIdentity].
func ProcessStartTicks(pid int) uint64 { return processStartTicks(pid) }

// ProcessRunning reports whether the process an identity names is still that
// process — not merely whether its pid is in use, which a reused pid would
// also be. known is false where this platform cannot tell (no start time was
// recorded, or none can be read), and then running means nothing: a caller
// deciding whether files are still in use must treat that as "maybe".
func ProcessRunning(p ProcessIdentity) (running, known bool) {
	if p.PID <= 0 || p.StartTicks == 0 {
		return false, false
	}
	stat, err := readProcStat(p.PID)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, true
	case err != nil:
		return false, false
	case stat.startTicks != p.StartTicks || stat.exited():
		return false, true
	}
	return true, true
}
