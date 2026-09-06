//go:build !windows

package job

import (
	"os/exec"
	"syscall"
)

// Killing a process *tree*, on the Unix targets.
//
// Killing the process the Companion started is not enough. A compiler that
// forks workers, or a launcher script that execs an engine, leaves children
// that outlive their parent and go on holding the CPU, the workspace and the
// files a user is waiting for. `cmd.Process.Kill` reaches exactly one pid.
//
// So every job is started in its own process group — Setpgid with no Pgid means
// "make a new group with this process as leader" — and a signal to the negative
// group id reaches every process in it, however deeply nested, as long as none
// of them called setpgid themselves.

// configureProcessGroup puts the child in a new process group of its own.
func configureProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalTree sends a signal to the whole group, falling back to the single
// process when the group is gone.
//
// ESRCH from the group call means every member has already exited, which is not
// an error: the caller asked for the tree to stop and the tree has stopped.
func signalTree(command *exec.Cmd, graceful bool) error {
	if command.Process == nil {
		return nil
	}
	signal := syscall.SIGKILL
	if graceful {
		// SIGTERM first, so a tool that writes a partial result on shutdown or
		// removes its own temporary files gets to do it. The forceful pass
		// follows after a grace period, from the executor.
		signal = syscall.SIGTERM
	}
	pid := command.Process.Pid
	if err := syscall.Kill(-pid, signal); err != nil {
		if err == syscall.ESRCH {
			return nil
		}
		// The group call can fail when the child changed its own group. The
		// single process is still worth signalling.
		return syscall.Kill(pid, signal)
	}
	return nil
}

// processGroupAlive reports whether any process in the job's group still
// exists. Signal 0 performs the permission and existence check without
// delivering anything, which is how the executor tells "the tree is gone" from
// "the leader exited and its children did not".
func processGroupAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(-pid, 0)
	return err == nil || err == syscall.EPERM
}
