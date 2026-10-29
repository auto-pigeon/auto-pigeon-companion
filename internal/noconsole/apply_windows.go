//go:build windows

package noconsole

import (
	"os/exec"
	"syscall"
)

// Apply makes command a background child: no console window. Call it after
// every other SysProcAttr setting, which it keeps.
func Apply(command *exec.Cmd) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.CreationFlags = Flags(command.SysProcAttr.CreationFlags)
}
