//go:build windows

package cli

import "syscall"

// detachedProcess starts a child with no console of its own, so the page
// `game open` raises outlives the handler that started it.
func detachedProcess() *syscall.SysProcAttr {
	const detached = 0x00000008 // DETACHED_PROCESS

	return &syscall.SysProcAttr{CreationFlags: detached}
}
