//go:build !linux

package job

import "errors"

// Outside Linux there is no cheap, dependency-free way to name a process
// precisely enough to signal it after a crash: no start time readable without
// a third-party module, and no boot id. So every question recovery asks the
// kernel answers "cannot be verified here", an abandoned job is marked
// interrupted, and nothing is ever signalled. A taskkill by pid alone is
// exactly the guess NEW_326 forbids.

var errNoProcessIdentity = errors.New("job: this platform cannot identify a process precisely enough to signal it after a restart")

type procStat struct {
	state      byte
	pgrp       int
	startTicks uint64
}

func (p procStat) exited() bool { return false }

func readProcStat(int) (procStat, error) { return procStat{}, errNoProcessIdentity }

func processStartTicks(int) uint64 { return 0 }

func readBootID() (string, error) { return "", errNoProcessIdentity }

func readPIDNamespace() (string, error) { return "", errNoProcessIdentity }

func readFileIdentity(string) (FileIdentity, error) { return FileIdentity{}, errNoProcessIdentity }

func readWorkingDirIdentity(int) (FileIdentity, error) { return FileIdentity{}, errNoProcessIdentity }
