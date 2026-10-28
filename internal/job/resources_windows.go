//go:build windows

package job

import (
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

// What the resource sampler asks Windows: the tool's own process, through a
// handle opened for query only — its kernel+user CPU time (GetProcessTimes)
// and its current working set (K32GetProcessMemoryInfo). No tasklist, no WMI,
// no privilege.
//
// Only the tool's own process. The executor starts a tool in a new process
// group and stops it with `taskkill /T`; it does not put it in a Job Object,
// so nothing here PROVES which other processes are the job's, and a parent-pid
// walk is the guess this design refuses. The reading says so: its scope is
// `root_process`. Its working set is resident memory; a committed-bytes figure
// (PagefileUsage) is not, and is never shown as RAM.

const processQueryLimitedInformation = 0x1000

var procGetProcessMemoryInfo = syscall.NewLazyDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

type windowsRoot struct {
	pid     int
	created int64 // the creation FILETIME, as the identity a pid lacks
}

func newTreeReader(pid int, _ uint64) (treeReader, string, error) {
	if pid <= 0 {
		return nil, "", errors.New("the program has no process id")
	}
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return nil, "", fmt.Errorf("the program's process could not be opened for reading: %w", err)
	}
	defer syscall.CloseHandle(handle)
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return nil, "", fmt.Errorf("the program's start time could not be read: %w", err)
	}
	return &windowsRoot{pid: pid, created: created.Nanoseconds()}, ScopeRootProcess, nil
}

func (w *windowsRoot) read() (map[procKey]procUsage, error) {
	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(w.pid))
	if err != nil {
		// No such process any more is the normal end; anything else is also
		// "cannot be read", and a reading never guesses which.
		return nil, errTreeExited
	}
	defer syscall.CloseHandle(handle)
	var code uint32
	if err := syscall.GetExitCodeProcess(handle, &code); err != nil || code != 259 /* STILL_ACTIVE */ {
		return nil, errTreeExited
	}
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return nil, fmt.Errorf("the program's CPU time could not be read: %w", err)
	}
	if created.Nanoseconds() != w.created {
		return nil, errTreeExited // the pid now names another process
	}
	counters := processMemoryCounters{cb: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	if ok, _, callErr := procGetProcessMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.cb)); ok == 0 {
		return nil, fmt.Errorf("the program's working set could not be read: %v", callErr)
	}
	filetime := func(ft syscall.Filetime) time.Duration {
		return time.Duration((int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)) * 100)
	}
	key := procKey{pid: w.pid, start: uint64(w.created)}
	return map[procKey]procUsage{key: {cpu: filetime(kernel) + filetime(user), rss: int64(counters.workingSetSize)}}, nil
}
