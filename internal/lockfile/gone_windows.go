//go:build windows

package lockfile

import "syscall"

// processGone reports that no process with pid exists, or that it has
// exited. Access denied is a process that exists.
func processGone(pid int) bool {
	const queryLimitedInformation, stillActive = 0x1000, 259
	handle, err := syscall.OpenProcess(queryLimitedInformation, false, uint32(pid))
	if err != nil {
		return err == syscall.Errno(87) // ERROR_INVALID_PARAMETER: no such process
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	if err := syscall.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code != stillActive
}
