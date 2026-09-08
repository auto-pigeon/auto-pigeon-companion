//go:build windows

package job

// ignoreTermination has nothing to ignore on Windows.
//
// There is no SIGTERM: proc_windows.go stops a tree through the job object,
// which a process cannot decline. The fixture that uses this is skipped there
// rather than pretending to measure an escalation that does not exist.
func ignoreTermination() {}

// processAlive is unused on Windows: the fixture that needs it is skipped
// there. It is declared so the package compiles for that target.
func processAlive(int) bool { return false }
