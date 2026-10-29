//go:build windows

package noconsole

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestApplyKeepsWhatWasSetAndAddsNoWindow(t *testing.T) {
	command := exec.Command("x")
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x200, HideWindow: true}
	Apply(command)
	if command.SysProcAttr.CreationFlags != 0x200|CreateNoWindow || !command.SysProcAttr.HideWindow {
		t.Fatalf("%+v", command.SysProcAttr)
	}
	bare := exec.Command("x")
	Apply(bare)
	if bare.SysProcAttr == nil || bare.SysProcAttr.CreationFlags != CreateNoWindow {
		t.Fatalf("%+v", bare.SysProcAttr)
	}
}
