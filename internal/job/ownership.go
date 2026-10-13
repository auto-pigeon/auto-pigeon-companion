package job

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Who may stop a job's programs after its supervisor has gone (NEW_326).
//
// Two facts, and recovery used to check only the first. IDENTITY: the kernel
// still has the very process the record names — same pid, same start time.
// OWNERSHIP: this store launched it, on this boot, and the supervisor that
// launched it is gone. A copy of a store has the first and never the second,
// and Q3_017E paid for the difference with a 44-minute LIGHT: a second
// Companion started on a copied sandbox read the copied record of a live job
// and SIGKILLed the original q3map2.
//
// So a recorded process is signalled only through an [ownedOrphan], and the
// only thing that makes one is [Store.assess] proving every row of the
// decision table below, immediately before the signal:
//
//	case                                         record        signal  outcome
//	same store, supervisor alive (stale beat ok)  unchanged     no      live_owner_retained
//	same store, supervisor gone, program running  interrupted   YES     owned_orphan_stopped
//	same store, supervisor gone, program gone     interrupted   no      already_exited
//	same store, supervisor gone, no program       interrupted   no      no_process
//	store copied, moved or restored               interrupted   no      foreign_store
//	another boot, machine or pid namespace        interrupted   no      foreign_boot
//	no launch record (older build), unreadable,
//	  reused pid, permission refused, non-Linux   interrupted   no      unverifiable
//
// Only the copy's own record changes: nothing here follows a path in a record
// back to the store that wrote it.

// osView is what recovery asks the operating system. A value, so a test can
// stand in for a kernel that refuses to answer — a permission error cannot be
// staged safely against a real process.
type osView struct {
	stat     func(pid int) (procStat, error)
	boot     func() (string, error)
	pidNS    func() (string, error)
	fileID   func(path string) (FileIdentity, error)
	cwdID    func(pid int) (FileIdentity, error)
	selfPID  func() int
	evalPath func(path string) (string, error)
}

var hostView = osView{
	stat: readProcStat, boot: readBootID, pidNS: readPIDNamespace, fileID: readFileIdentity,
	cwdID: readWorkingDirIdentity, selfPID: os.Getpid, evalPath: filepath.EvalSymlinks,
}

// launch records the provenance of a job this process is admitting or
// starting. A fact it cannot read is left empty, and an empty fact is one
// recovery will refuse to act on: the record never claims more than was seen.
//
// workDir is the started program's working directory, "" at admission.
func (s *Store) launch(id, workDir string) *Launch {
	view := s.view()
	l := &Launch{}
	if canonical, err := view.evalPath(s.root); err == nil {
		l.Store = canonical
	}
	l.StoreFile, _ = view.fileID(s.root)
	if dir, err := s.dirFor(id); err == nil {
		if err := os.MkdirAll(dir, 0o700); err == nil {
			l.JobFile, _ = view.fileID(dir)
		}
		if workDir != "" {
			if rel, err := filepath.Rel(dir, workDir); err == nil && rel != ".." &&
				!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
				l.WorkDir = filepath.ToSlash(rel)
			}
		}
	}
	l.Boot, _ = view.boot()
	l.PIDNamespace, _ = view.pidNS()
	self := view.selfPID()
	l.Supervisor = ProcessIdentity{PID: self}
	if stat, err := view.stat(self); err == nil {
		l.Supervisor.StartTicks = stat.startTicks
	}
	return l
}

func (s *Store) view() osView {
	if s.os != nil {
		return *s.os
	}
	return hostView
}

// ownedOrphan is the proof [killAbandonedTree] requires: a process group this
// store launched, whose supervisor is gone, whose leader is still the process
// it started. Its field is unexported and only [Store.assess] sets it.
type ownedOrphan struct{ pid int }

// assessment is one row of the table, and a proof when — only when — the row
// permits a signal.
type assessment struct {
	outcome RecoveryOutcome
	reason  string
	orphan  *ownedOrphan
}

// assess classifies one abandoned-looking job. It reads the store and the
// kernel as they are NOW; it never follows a path in the record anywhere.
func (s *Store) assess(j *Job) assessment {
	view := s.view()
	l := j.Launch
	if l == nil {
		return assessment{outcome: RecoveryUnverifiable, reason: "no_launch_record"}
	}
	if l.Store == "" || l.StoreFile == (FileIdentity{}) || l.JobFile == (FileIdentity{}) ||
		l.Boot == "" || l.PIDNamespace == "" || l.Supervisor.PID <= 0 || l.Supervisor.StartTicks == 0 {
		return assessment{outcome: RecoveryUnverifiable, reason: "launch_record_incomplete"}
	}

	// Where and when this process is: the boot, the namespace.
	boot, err := view.boot()
	if err != nil {
		return assessment{outcome: RecoveryUnverifiable, reason: "boot_id_unreadable"}
	}
	namespace, err := view.pidNS()
	if err != nil {
		return assessment{outcome: RecoveryUnverifiable, reason: "pid_namespace_unreadable"}
	}
	if boot != l.Boot {
		return assessment{outcome: RecoveryForeignBoot, reason: "another_boot_or_machine"}
	}
	if namespace != l.PIDNamespace {
		return assessment{outcome: RecoveryForeignBoot, reason: "another_pid_namespace"}
	}

	// Which store is reading this: its canonical path and its inodes, now.
	canonical, err := view.evalPath(s.root)
	if err != nil {
		return assessment{outcome: RecoveryUnverifiable, reason: "store_path_unreadable"}
	}
	storeFile, err := view.fileID(s.root)
	if err != nil {
		return assessment{outcome: RecoveryUnverifiable, reason: "store_identity_unreadable"}
	}
	dir, err := s.dirFor(j.ID)
	if err != nil {
		return assessment{outcome: RecoveryUnverifiable, reason: "job_id_invalid"}
	}
	jobFile, err := view.fileID(dir)
	if err != nil {
		return assessment{outcome: RecoveryUnverifiable, reason: "job_identity_unreadable"}
	}
	switch {
	case canonical != l.Store:
		// A move keeps the inode and changes the path; a moved store is not
		// permission to stop what the old location started.
		return assessment{outcome: RecoveryForeignStore, reason: "store_path_differs"}
	case storeFile != l.StoreFile:
		return assessment{outcome: RecoveryForeignStore, reason: "store_is_a_copy"}
	case jobFile != l.JobFile:
		return assessment{outcome: RecoveryForeignStore, reason: "job_directory_is_a_copy"}
	}

	// The supervisor that launched it: alive means it is still its job, even
	// with a stale heartbeat — a laptop asleep, a SIGSTOP, an overloaded host
	// or a clock that jumped. Gone is "no such process", a zombie, or the pid
	// now naming a process started at another time. Anything else is an
	// inability to look.
	supervisor, err := view.stat(l.Supervisor.PID)
	switch {
	case err == nil && !supervisor.exited() && supervisor.startTicks == l.Supervisor.StartTicks:
		return assessment{outcome: RecoveryLiveOwnerRetained, reason: "supervisor_alive"}
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return assessment{outcome: RecoveryUnverifiable, reason: "supervisor_uninspectable"}
	}

	// The supervisor is gone. Now the program it started, if it started one.
	if j.Process.PID <= 0 {
		return assessment{outcome: RecoveryNoProcess, reason: "never_started"}
	}
	if j.Process.StartTicks == 0 {
		return assessment{outcome: RecoveryUnverifiable, reason: "child_start_unrecorded"}
	}
	child, err := view.stat(j.Process.PID)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return assessment{outcome: RecoveryAlreadyExited, reason: "child_gone"}
	case err != nil:
		return assessment{outcome: RecoveryUnverifiable, reason: "child_uninspectable"}
	case child.startTicks != j.Process.StartTicks:
		// The pid names another process now. Not ours to touch, and not proof
		// that ours exited cleanly either.
		return assessment{outcome: RecoveryUnverifiable, reason: "child_start_mismatch"}
	case child.exited():
		return assessment{outcome: RecoveryAlreadyExited, reason: "child_exited"}
	case child.pgrp != j.Process.PID:
		// The group signal goes to -pid; a leader that left its group would
		// make that somebody else's group.
		return assessment{outcome: RecoveryUnverifiable, reason: "child_left_its_group"}
	}
	if l.WorkDir != "" {
		// The process is still IN this store's job directory. A store restored
		// to the same path after the original was deleted can reuse inode
		// numbers, but never the inode the orphan is sitting in: the kernel
		// keeps that one allocated. A tool that changed directory is read as
		// unverifiable, never as permission.
		here, err := view.fileID(filepath.Join(dir, filepath.FromSlash(l.WorkDir)))
		if err != nil {
			return assessment{outcome: RecoveryUnverifiable, reason: "working_directory_missing"}
		}
		running, err := view.cwdID(j.Process.PID)
		switch {
		case err != nil:
			return assessment{outcome: RecoveryUnverifiable, reason: "child_working_directory_uninspectable"}
		case running != here:
			return assessment{outcome: RecoveryUnverifiable, reason: "child_working_directory_differs"}
		}
	}
	return assessment{outcome: RecoveryOwnedOrphanStopped, reason: "supervisor_gone", orphan: &ownedOrphan{pid: j.Process.PID}}
}

// recoveryNote is what a user reads on a job recovery ended. None of them
// claims to know how the program ended, and only the one that signalled says
// anything was stopped.
func recoveryNote(state State, a assessment, stopped bool) string {
	switch a.outcome {
	case RecoveryOwnedOrphanStopped:
		if stopped {
			return "the Companion running this job stopped while it was " + string(state) +
				"; its programs were still running with nobody supervising them, and were stopped; nothing here knows how it ended"
		}
		return "the Companion running this job stopped while it was " + string(state) +
			"; its programs were still running and could not be stopped (" + a.reason + "); nothing here knows how it ended"
	case RecoveryAlreadyExited:
		return "the Companion running this job stopped while it was " + string(state) +
			"; its programs had already exited; nothing here knows how it ended"
	case RecoveryNoProcess:
		return "the Companion stopped before this job started a program; nothing was run"
	case RecoveryForeignStore:
		return "this record was copied, moved or restored here from another job store while it said " + string(state) +
			"; no program was stopped, and nothing here knows how that run ended"
	case RecoveryForeignBoot:
		return "this job was started on another machine, or before this machine restarted, while it said " + string(state) +
			"; no program was stopped, and nothing here knows how it ended"
	}
	return "this job said " + string(state) + " and who was running it could not be verified (" + a.reason +
		"); no program was stopped, and nothing here knows how it ended"
}
