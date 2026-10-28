//go:build linux

package job

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// What the resource sampler asks the Linux kernel: /proc/<pid>/stat for each
// process the job owns, and /proc/<pid>/task/<tid>/children to find them. No
// shell, no `ps`, nothing that needs a privilege the Companion does not have.
//
// Ownership is the process group. The executor starts every tool as the
// leader of a new group (configureProcessGroup), so the group's id is the
// tool's pid and a process in it is the tool or something it started that did
// not leave. A descendant is counted only when it is still in that group and
// was started after the tool — an unrelated process that happens to hold a
// reused pid fails one or the other.

// userHZ is the unit of utime and stime in /proc/<pid>/stat: USER_HZ, which is
// 100 on every architecture Linux has shipped for the last two decades and is
// what `getconf CLK_TCK` prints. It is not sysconf'd here because that would
// need cgo; a kernel built otherwise would scale every CPU figure, and the
// sampler test pins the figure against an independent reading.
const userHZ = 100

type linuxTree struct {
	root     int
	start    uint64
	pageSize int64
}

func newTreeReader(pid int, startTicks uint64) (treeReader, string, error) {
	if pid <= 0 || startTicks == 0 {
		return nil, "", errors.New("the program's start time could not be read, so its processes cannot be told from others")
	}
	return &linuxTree{root: pid, start: startTicks, pageSize: int64(os.Getpagesize())}, ScopeProcessGroup, nil
}

type usageStat struct {
	state byte
	pgrp  int
	start uint64
	cpu   time.Duration
	rss   int64
}

func (t *linuxTree) read() (map[procKey]procUsage, error) {
	root, err := t.stat(t.root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, errTreeExited
	case err != nil:
		return nil, err
	case root.start != t.start, root.state == 'Z', root.state == 'X':
		// Gone, or reaped and the pid given to something else.
		return nil, errTreeExited
	}
	out := map[procKey]procUsage{{t.root, root.start}: {cpu: root.cpu, rss: root.rss}}
	queue := []int{t.root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, child := range t.children(pid) {
			st, err := t.stat(child)
			if err != nil || st.pgrp != t.root || st.start < t.start || st.state == 'Z' || st.state == 'X' {
				continue
			}
			key := procKey{child, st.start}
			if _, seen := out[key]; seen {
				continue
			}
			out[key] = procUsage{cpu: st.cpu, rss: st.rss}
			queue = append(queue, child)
		}
	}
	return out, nil
}

// children lists a process's children across all of its threads. A thread
// that exited between the listing and the read simply contributes none.
func (t *linuxTree) children(pid int) []int {
	base := "/proc/" + strconv.Itoa(pid) + "/task"
	tasks, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []int
	for _, task := range tasks {
		raw, err := os.ReadFile(base + "/" + task.Name() + "/children")
		if err != nil {
			continue
		}
		for _, field := range strings.Fields(string(raw)) {
			if child, err := strconv.Atoi(field); err == nil && child > 0 {
				out = append(out, child)
			}
		}
	}
	return out
}

func (t *linuxTree) stat(pid int) (usageStat, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			return usageStat{}, fmt.Errorf("process %d: %w", pid, fs.ErrNotExist)
		}
		if errors.Is(err, fs.ErrPermission) {
			return usageStat{}, fmt.Errorf("reading process %d is not permitted here", pid)
		}
		return usageStat{}, err
	}
	return parseUsageStat(string(raw), t.pageSize)
}

// parseUsageStat reads the fields the sampler needs from one /proc/<pid>/stat
// line. The command name is skipped by its LAST ')' because a program may put
// spaces and parentheses in its own name.
func parseUsageStat(line string, pageSize int64) (usageStat, error) {
	end := strings.LastIndexByte(line, ')')
	if end < 0 {
		return usageStat{}, errors.New("a /proc stat line has no command field")
	}
	fields := strings.Fields(line[end+1:])
	if len(fields) < 22 || len(fields[0]) != 1 {
		return usageStat{}, errors.New("a /proc stat line is short")
	}
	number := func(i int) (uint64, error) { return strconv.ParseUint(fields[i], 10, 64) }
	pgrp, err := strconv.Atoi(fields[2])
	if err != nil {
		return usageStat{}, err
	}
	utime, err1 := number(11)
	stime, err2 := number(12)
	start, err3 := number(19)
	rssPages, err4 := strconv.ParseInt(fields[21], 10, 64)
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		return usageStat{}, fmt.Errorf("a /proc stat line: %w", err)
	}
	if rssPages < 0 {
		rssPages = 0
	}
	ticks := utime + stime
	return usageStat{
		state: fields[0][0],
		pgrp:  pgrp,
		start: start,
		cpu:   time.Duration(ticks) * time.Second / userHZ,
		rss:   rssPages * pageSize,
	}, nil
}
