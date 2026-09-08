//go:build linux

package job

import (
	"os"
	"strconv"
	"strings"
)

// groupMembers counts the processes in one process group, or -1 where the
// operating system will not say.
//
// /proc/<pid>/stat field 5 is the process group id. Reading it is Linux-only on
// purpose: this is the enrichment that turns "the group is alive" into "the
// group had a leader, a child and a grandchild, and now has none", and a
// platform that cannot answer says so rather than having a weaker check
// substituted for it. `AUCOM/AUT 229` asks for exactly that distinction.
func groupMembers(pgid int) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return -1
	}
	count := 0
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		data, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue // It exited between the listing and the read.
		}
		// The comm field is parenthesised and may contain spaces, so the fields
		// after it are counted from the last ')' rather than from the start.
		close := strings.LastIndexByte(string(data), ')')
		if close < 0 {
			continue
		}
		fields := strings.Fields(string(data)[close+1:])
		// After ')': state, ppid, pgrp — so pgrp is the third.
		if len(fields) < 3 {
			continue
		}
		if group, err := strconv.Atoi(fields[2]); err == nil && group == pgid {
			count++
		}
	}
	return count
}
