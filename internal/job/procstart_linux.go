//go:build linux

package job

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// What recovery asks the Linux kernel, and nothing else.
//
// Every answer is either a fact or an error that says why there is none. "No
// such process" is the one error recovery may act on — it means the process is
// gone — and it is returned wrapped in [fs.ErrNotExist]; anything else (a
// permission refused by a hidepid mount, a short read) is an inability to
// look, and recovery never treats that as permission to signal.

// procStat is the part of /proc/<pid>/stat recovery reads.
type procStat struct {
	// state is field 3: Z (zombie) and X (dead) are a process that has exited
	// and not yet been reaped, which is not a process that is running.
	state byte
	// pgrp is field 5, the process group.
	pgrp int
	// startTicks is field 22: when the kernel started it, in clock ticks since
	// THIS boot — which is why a boot id is recorded beside it.
	startTicks uint64
}

func (p procStat) exited() bool { return p.state == 'Z' || p.state == 'X' }

// readProcStat reads one process's stat line.
func readProcStat(pid int) (procStat, error) {
	if pid <= 0 {
		return procStat{}, fmt.Errorf("job: %d is not a process id: %w", pid, fs.ErrNotExist)
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			return procStat{}, fmt.Errorf("job: process %d: %w", pid, fs.ErrNotExist)
		}
		return procStat{}, err
	}
	// The command name (field 2) is in parentheses and may itself contain
	// spaces or parentheses, so the fields are counted after the LAST ")".
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return procStat{}, fmt.Errorf("job: /proc/%d/stat has no command field", pid)
	}
	fields := strings.Fields(string(raw)[end+1:])
	// fields[0] is field 3 (state), fields[2] field 5, fields[19] field 22.
	if len(fields) < 20 || len(fields[0]) != 1 {
		return procStat{}, fmt.Errorf("job: /proc/%d/stat is short", pid)
	}
	pgrp, err := strconv.Atoi(fields[2])
	if err != nil {
		return procStat{}, fmt.Errorf("job: /proc/%d/stat: %w", pid, err)
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || ticks == 0 {
		return procStat{}, fmt.Errorf("job: /proc/%d/stat has no start time", pid)
	}
	return procStat{state: fields[0][0], pgrp: pgrp, startTicks: ticks}, nil
}

// processStartTicks is when the kernel started pid, or zero when that cannot
// be read — which a launch record then carries as "cannot be verified".
func processStartTicks(pid int) uint64 {
	stat, err := readProcStat(pid)
	if err != nil {
		return 0
	}
	return stat.startTicks
}

// readBootID is the kernel's random id for this boot. Start ticks count from
// boot, so the same pid and ticks on another boot — or another machine sharing
// the store over a network — are a different process.
func readBootID() (string, error) {
	raw, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(raw))
	if id == "" {
		return "", errors.New("job: the kernel's boot id is empty")
	}
	return id, nil
}

// readPIDNamespace names the pid namespace this process is in, e.g.
// `pid:[4026531836]`. A pid means something only inside its namespace.
func readPIDNamespace() (string, error) {
	return os.Readlink("/proc/self/ns/pid")
}

// readFileIdentity is a directory's device and inode, after the symlinks.
func readFileIdentity(path string) (FileIdentity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileIdentity{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return FileIdentity{}, fmt.Errorf("job: %s has no inode here", filepath.Base(path))
	}
	return FileIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

// readWorkingDirIdentity is the device and inode of the directory a process is
// running in, read through /proc/<pid>/cwd — which names the directory the
// kernel holds, even one that has since been deleted or renamed.
func readWorkingDirIdentity(pid int) (FileIdentity, error) {
	if pid <= 0 {
		return FileIdentity{}, fmt.Errorf("job: %d is not a process id: %w", pid, fs.ErrNotExist)
	}
	return readFileIdentity("/proc/" + strconv.Itoa(pid) + "/cwd")
}
