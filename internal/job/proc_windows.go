//go:build windows

package job

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// Killing a process *tree*, on Windows.
//
// The Unix answer — a process group and a negative-pid signal — has no direct
// equivalent here. The equivalent that does exist is a Job Object, and reaching
// one needs golang.org/x/sys/windows: `syscall` does not expose
// CreateJobObject, and this repository takes no third-party dependencies.
//
// So the child is started in a new process group, which is what makes it
// separable from the Companion's own console, and the tree is taken down with
// taskkill's own /T. taskkill is a system program run the same way every other
// program here is run — an absolute path and an argument array, never a shell —
// and it is the documented Windows mechanism for exactly this.
//
// The absolute path matters: `taskkill` resolved through a search path is a
// program chosen by whatever is first on PATH, which is the thing this package
// exists to avoid.

// createNewProcessGroup is CREATE_NEW_PROCESS_GROUP. Declared here because
// syscall's Windows constants do not include it.
const createNewProcessGroup = 0x00000200

func configureProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// taskkillPath is the absolute path to taskkill.exe, from the system root.
func taskkillPath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "taskkill.exe")
}

func signalTree(command *exec.Cmd, graceful bool) error {
	if command.Process == nil {
		return nil
	}
	args := []string{"/T", "/PID", strconv.Itoa(command.Process.Pid)}
	if !graceful {
		args = append([]string{"/F"}, args...)
	}
	kill := exec.Command(taskkillPath(), args...)
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	// taskkill exits nonzero when the process has already gone, which is the
	// outcome the caller wanted; its status is deliberately not an error here.
	_ = kill.Run()
	return nil
}

// processGroupAlive reports whether the process still exists.
//
// There is no group-wide equivalent without a Job Object, so this answers for
// the leader. The executor uses it only as a hint before escalating from a
// graceful stop to a forceful one.
func processGroupAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	if err := syscall.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	const stillActive = 259
	return code == stillActive
}

// killAbandonedTree is never reached on Windows: processStartTicks returns zero
// there, so recovery cannot prove a pid is still the job's and signals nothing.
func killAbandonedTree(pid int) error { return nil }
