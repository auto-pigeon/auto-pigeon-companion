// Package oslock is the operating system's exclusive lock on an open file:
// flock on Linux, macOS and the BSDs, LockFileEx on Windows (called from
// kernel32, as this program's other Windows calls are: it has no
// dependencies). The kernel lets go of it when the holder's process ends, so
// a dead holder never needs recovering — which is what a lock FILE's
// pathname cannot give (NEW_368: two processes that judged one lock file
// abandoned could each remove whatever then occupied its path).
//
// It is advisory and per host: it excludes cooperating processes on this
// machine that lock the same file. It is not relied on across machines or on
// network file systems, where flock may be emulated or ignored; the files
// locked here live in the Companion's own local folders. Callers keep the
// file open for as long as they hold the lock, and never remove it.
//
// It started as the guard of the tools folder's gates (internal/tooluse) and
// is shared with internal/lockfile, which tooluse itself imports.
package oslock
