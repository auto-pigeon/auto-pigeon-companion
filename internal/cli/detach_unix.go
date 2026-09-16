//go:build !windows

package cli

import "syscall"

// detachedProcess starts a child in its own session, so the page `game open`
// raises is not killed when the desktop reaps the handler that started it.
func detachedProcess() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
