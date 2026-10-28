//go:build windows

package selfupdate

import (
	"os/exec"
	"syscall"
)

// detach starts the new Companion in its own process group with no console
// of its own, so it outlives the one that started it.
func detach(command *exec.Cmd) {
	const createNewProcessGroup, detachedProcess = 0x00000200, 0x00000008
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess}
}
