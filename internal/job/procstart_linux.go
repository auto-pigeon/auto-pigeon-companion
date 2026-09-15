//go:build linux

package job

import (
	"os"
	"strconv"
	"strings"
)

// processStartTicks is when the kernel started pid, in clock ticks since boot:
// field 22 of /proc/<pid>/stat. Zero when there is no such process or the file
// cannot be read, which recovery treats as "cannot verify".
func processStartTicks(pid int) uint64 {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	// The command name (field 2) is in parentheses and may itself contain
	// spaces or parentheses, so the fields are counted after the LAST ")".
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return 0
	}
	fields := strings.Fields(string(raw)[end+1:])
	// fields[0] is field 3 (state), so field 22 is fields[19].
	if len(fields) < 20 {
		return 0
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0
	}
	return ticks
}
