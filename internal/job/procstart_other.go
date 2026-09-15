//go:build !linux

package job

// processStartTicks has no cheap, dependency-free source outside Linux, so an
// abandoned process there is never verified and never signalled by recovery.
// The job is still marked interrupted.
func processStartTicks(pid int) uint64 { return 0 }
