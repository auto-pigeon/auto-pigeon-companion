//go:build unix

package selfupdate

import (
	"os/exec"
	"syscall"
)

// detach makes the new Companion its own session, so it outlives the one
// that started it and is not stopped with that one's terminal.
func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
