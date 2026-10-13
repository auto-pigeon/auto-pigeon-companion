//go:build linux

package job

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
)

// NEW_326's acceptance rows R01 and R04–R16. R02 and R03 are in
// ownership_linux_test.go with the supervisor fixture they share.

func ageHeartbeat(t *testing.T, store *Store, id string) {
	t.Helper()
	if err := store.Heartbeat(id, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
}

// grantedService is a Service over a store with the supervisor's profiles
// granted, as the Companion that owns that store would have them.
func grantedService(t *testing.T, store *Store, profiles string, logf func(string, ...any)) *Service {
	t.Helper()
	catalog := NewCatalog(profiles)
	grants := &grantSource{bindings: map[string]binding.LocalBinding{}}
	entries, err := catalog.readDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		grants.grantEverything(t, entry)
	}
	if logf == nil {
		logf = func(format string, args ...any) { t.Logf("service: "+format, args...) }
	}
	service, err := NewService(Options{Store: store, Catalog: catalog, Bindings: grants.lookup, Concurrency: 1, Logf: logf})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func decidedFor(recovered []Recovered, id string) (Recovered, bool) {
	for _, r := range recovered {
		if r.ID == id {
			return r, true
		}
	}
	return Recovered{}, false
}

// R01 — the positive control. The genuine store's supervisor is killed -9 with
// its build at the barrier; a Companion restarted on that same store stops the
// abandoned tree, marks the job interrupted, keeps its workspace, and runs
// nothing again. Without this row, deleting every kill would pass R02.
func TestR01RestartingTheGenuineStoreStopsItsOwnAbandonedBuild(t *testing.T) {
	s := startSupervisor(t)
	s.crash()
	s.requireBuildAlive("the supervisor's crash") // the orphan is really there
	store := s.store()
	ageHeartbeat(t, store, s.id)

	var mu sync.Mutex
	var lines []string
	service := grantedService(t, store, filepath.Join(s.root, "profiles"), func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	s.requireBuildGone("restarting the genuine store")
	j, err := store.Load(s.id)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != Interrupted || j.Recovery == nil || j.Recovery.Outcome != RecoveryOwnedOrphanStopped || !j.Recovery.Signalled {
		t.Fatalf("state %s, recovery %+v", j.State, j.Recovery)
	}
	if !strings.Contains(j.Error, "were stopped") || !strings.Contains(j.Error, "nothing here knows how it ended") {
		t.Errorf("error %q", j.Error)
	}
	if jobs, _ := store.List(); len(jobs) != 1 {
		t.Errorf("a restart created %d jobs; it must run nothing again", len(jobs))
	}
	if _, err := os.Stat(s.workspace()); err != nil {
		t.Errorf("the interrupted job's workspace is gone: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if joined := strings.Join(lines, "\n"); !strings.Contains(joined, "recovery owned_orphan_stopped (supervisor_gone)") {
		t.Errorf("the service did not log the decision:\n%s", joined)
	}
}

// R04 — the copy's PERIODIC recovery, after a start-up pass that found the
// copied heartbeat still fresh: the job is taken later, and nothing is
// signalled then either.
func TestR04PeriodicRecoveryOfACopyNeverSignalsTheOriginal(t *testing.T) {
	s := startSupervisor(t)
	record, err := os.ReadFile(filepath.Join(s.jobs, s.id, recordName))
	if err != nil {
		t.Fatal(err)
	}
	copyRoot := filepath.Join(t.TempDir(), "copied", "jobs")
	copyStore(t, s.jobs, copyRoot)
	copied, err := OpenStore(copyRoot)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Options{Store: copied, Catalog: NewCatalog(t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	service.beat, service.recoverEvery = 20*time.Millisecond, 1
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if j, _ := copied.Load(s.id); j.State != Running {
		t.Fatalf("start-up took a job whose copied heartbeat was fresh: %s", j.State)
	}

	ageHeartbeat(t, copied, s.id)
	deadline := time.Now().Add(10 * time.Second)
	for {
		j, _ := copied.Load(s.id)
		if j.State == Interrupted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the periodic pass never took the copied job")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.requireBuildAlive("the copy's periodic recovery")
	time.Sleep(20 * 20 * time.Millisecond) // twenty more passes
	s.requireBuildAlive("later periodic passes on the copy")
	s.requireOriginalUntouched(record)
	requireCopiedHistory(t, copied, s.id)
	s.finishOriginal()
}

// R05 — copy, THEN the original supervisor dies with its build still running.
// The copy still has no authority; the original store's own recovery stops it.
func TestR05AfterTheOriginalSupervisorDiesOnlyTheOriginalStoreCleansUp(t *testing.T) {
	s := startSupervisor(t)
	copied, _ := copyActiveStore(t, s)
	s.crash()

	recovered, err := copied.Recover(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := decidedFor(recovered, s.id); r.Outcome != RecoveryForeignStore || r.Signalled {
		t.Fatalf("the copy decided %+v", r)
	}
	s.requireBuildAlive("recovering the copy of a dead supervisor's store")

	store := s.store()
	ageHeartbeat(t, store, s.id)
	recovered, err = store.Recover(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := decidedFor(recovered, s.id); r.Outcome != RecoveryOwnedOrphanStopped || !r.Signalled {
		t.Fatalf("the original store decided %+v", r)
	}
	s.requireBuildGone("recovering the original store")
}

// R06 — a genuine live supervisor whose heartbeat looks stale (a clock jumped
// an hour, a laptop slept, a SIGSTOP): another process on the SAME store
// leaves its job alone, and so does its own recovery when it omits its ids.
func TestR06ALiveSupervisorKeepsItsBuildWhateverItsHeartbeatSays(t *testing.T) {
	s := startSupervisor(t)
	record, err := os.ReadFile(filepath.Join(s.jobs, s.id, recordName))
	if err != nil {
		t.Fatal(err)
	}
	// An hour in the future: every heartbeat is stale to this pass.
	recovered, err := s.store().Recover(time.Now().Add(time.Hour).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := decidedFor(recovered, s.id); r.Outcome != RecoveryLiveOwnerRetained || r.Interrupted || r.Signalled {
		t.Fatalf("decided %+v, want live_owner_retained and nothing changed", r)
	}
	s.requireBuildAlive("a recovery pass on the same store")
	s.requireOriginalUntouched(record)

	// The supervised-ID omission: the owner's OWN pass, without its ids.
	h := newHarness(t, fixtureProfile(t, "test.own", barrierAction()))
	submitted, err := h.service.Submit(h.helperRequest("test.own", "barrier"))
	if err != nil {
		t.Fatal(err)
	}
	running := waitForState(t, h, submitted.ID, Running)
	ready := filepath.Join(layoutFor(h.store.Root(), submitted.ID).Workspace, "ready")
	for deadline := time.Now().Add(30 * time.Second); ; {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the in-process barrier never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ticks := processStartTicks(running.Process.PID)
	own, err := h.store.Recover(time.Now().Add(time.Hour).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := decidedFor(own, submitted.ID); r.Outcome != RecoveryLiveOwnerRetained || r.Interrupted {
		t.Fatalf("its own pass decided %+v", r)
	}
	time.Sleep(150 * time.Millisecond)
	if !stillRunning(running.Process.PID, ticks) {
		t.Fatal("its own recovery pass stopped its own build")
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(ready), "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if done := h.waitFor(submitted.ID); done.State != Succeeded {
		t.Fatalf("its own build ended %s: %s", done.State, done.Error)
	}
	s.finishOriginal()
}

// R08 — the right pid and start ticks, recorded on another boot (or another
// machine sharing the store, or another pid namespace): never signalled.
func TestR08AnotherBootsPidAndTicksAreNeverSignalled(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	tree := startTree(t)
	pid := tree.Process.Pid
	identity := ProcessIdentity{PID: pid, StartTicks: processStartTicks(pid)}
	for _, mutate := range []func(*Launch){
		func(l *Launch) { l.Boot = "00000000-0000-0000-0000-000000000000" },
		func(l *Launch) { l.PIDNamespace = "pid:[1]" },
	} {
		id := abandonedJob(t, store, identity, goneSupervisor(t))
		j, _ := store.Load(id)
		mutate(j.Launch)
		if err := store.Save(j); err != nil {
			t.Fatal(err)
		}
	}
	recovered, err := store.Recover(time.Now().UTC())
	if err != nil || len(recovered) != 2 {
		t.Fatalf("recovered %+v (%v)", recovered, err)
	}
	for _, r := range recovered {
		if r.Outcome != RecoveryForeignBoot || r.Signalled {
			t.Errorf("%+v, want foreign_boot and nothing signalled", r)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if !processGroupAlive(pid) {
		t.Fatal("a pid and start time from another boot were signalled")
	}
}

// R09 — a record an older build wrote (aucom.job/1.4, no `launch`) naming a
// live process with the right start time: it still loads and lists, and it
// proves no ownership, so nothing is signalled.
func TestR09ALegacyRecordIsReadableAndHasNoSignalAuthority(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	tree := startTree(t)
	pid := tree.Process.Pid
	id, _ := NewID(time.Now())
	legacy := fmt.Sprintf(`{
  "schema_version": "aucom.job/1.4",
  "id": %q,
  "state": "running",
  "request": {"profile_id": "test.legacy", "action_id": "light"},
  "stdout": {"bytes": 0, "stored": 0, "dropped": 0, "lines": 0, "truncated": false},
  "stderr": {"bytes": 0, "stored": 0, "dropped": 0, "lines": 0, "truncated": false},
  "created_at": "2026-10-12T19:00:00Z",
  "owner": {"pid": 999999},
  "process": {"pid": %d, "start_ticks": %d},
  "history": [{"state": "queued", "at": "2026-10-12T19:00:00Z"}, {"state": "running", "at": "2026-10-12T19:00:01Z", "note": "pid %d"}]
}
`, id, pid, processStartTicks(pid), pid)
	if err := os.MkdirAll(filepath.Join(store.Root(), id), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), id, recordName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if jobs, err := store.List(); err != nil || len(jobs) != 1 || jobs[0].Launch != nil {
		t.Fatalf("the legacy record does not list: %v %v", jobs, err)
	}
	recovered, err := store.Recover(time.Now().UTC())
	if err != nil || len(recovered) != 1 || recovered[0].Outcome != RecoveryUnverifiable ||
		recovered[0].Reason != "no_launch_record" || recovered[0].Signalled {
		t.Fatalf("recovered %+v (%v)", recovered, err)
	}
	time.Sleep(200 * time.Millisecond)
	if !processGroupAlive(pid) {
		t.Fatal("a legacy record's pid was signalled")
	}
	after, err := store.Load(id)
	if err != nil || after.State != Interrupted || len(after.History) != 3 || !strings.Contains(after.Error, "no program was stopped") {
		t.Fatalf("after: %+v (%v)", after, err)
	}
}

// R10 — Retry of the copied, interrupted record: a NEW job, launched and owned
// by the Companion that retried it, which runs normally — and the original
// build, whose pid the old record names, is not touched.
func TestR10RetryingACopiedRecordIsANewJobWithItsOwnProvenance(t *testing.T) {
	s := startSupervisor(t)
	copied, _ := copyActiveStore(t, s)
	service := grantedService(t, copied, filepath.Join(s.root, "profiles"), nil)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	requireCopiedHistory(t, copied, s.id)

	retry, err := service.Retry(s.id)
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID == s.id || retry.Request.RetryOf != s.id {
		t.Fatalf("the retry is %s (retry of %q)", retry.ID, retry.Request.RetryOf)
	}
	deadline := time.Now().Add(30 * time.Second)
	var running *Job
	for {
		j, _ := copied.Load(retry.ID)
		if j != nil && j.State == Running && j.Process.PID != 0 {
			running = j
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the retry never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	canonical, _ := filepath.EvalSymlinks(copied.Root())
	if running.Launch == nil || running.Launch.Store != canonical || running.Launch.Supervisor.PID != os.Getpid() ||
		running.Process.PID == s.leader || running.Launch.WorkDir != "workspace" {
		t.Fatalf("the retry's provenance is %+v, process %+v", running.Launch, running.Process)
	}
	s.requireBuildAlive("retrying the copied record")
	if err := os.WriteFile(filepath.Join(layoutFor(copied.Root(), retry.ID).Workspace, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if done, err := service.Wait(ctx, retry.ID); err != nil || done.State != Succeeded {
		t.Fatalf("the retry ended %v (%v)", done, err)
	}
	s.requireBuildAlive("the retry finishing")
	s.finishOriginal()
}

// R11 — the genuine store reached through a symlink alias is still the genuine
// store; the actual copied directory is not.
func TestR11ASymlinkAliasIsTheSameStoreAndACopyIsNot(t *testing.T) {
	s := startSupervisor(t)
	alias := filepath.Join(t.TempDir(), "alias-to-jobs")
	if err := os.Symlink(s.jobs, alias); err != nil {
		t.Fatal(err)
	}
	copied, _ := copyActiveStore(t, s)
	s.crash()

	recovered, err := copied.Recover(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := decidedFor(recovered, s.id); r.Outcome != RecoveryForeignStore {
		t.Fatalf("the copy decided %+v", r)
	}
	s.requireBuildAlive("recovering the copy")

	aliased, err := OpenStore(alias)
	if err != nil {
		t.Fatal(err)
	}
	ageHeartbeat(t, aliased, s.id)
	recovered, err = aliased.Recover(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := decidedFor(recovered, s.id); r.Outcome != RecoveryOwnedOrphanStopped || !r.Signalled {
		t.Fatalf("the alias decided %+v", r)
	}
	s.requireBuildGone("recovering the genuine store through its alias")
}

// R12 — what cannot be read is said, never guessed: a permission refusal on
// the supervisor or the program, an unreadable boot id, a launch record with a
// field missing, a program that has already gone. The kernel's refusals are
// injected — a real one cannot be staged safely — and the process they are
// about is real and must survive.
func TestR12WhatCannotBeReadIsUnverifiableAndNeverSignalled(t *testing.T) {
	tree := startTree(t)
	pid := tree.Process.Pid
	identity := ProcessIdentity{PID: pid, StartTicks: processStartTicks(pid)}
	refuse := func(target int) func(int) (procStat, error) {
		return func(p int) (procStat, error) {
			if p == target {
				return procStat{}, fmt.Errorf("open /proc/%d/stat: %w", p, fs.ErrPermission)
			}
			return readProcStat(p)
		}
	}
	gone := goneSupervisor(t)
	cases := []struct {
		name    string
		launch  *Launch
		process ProcessIdentity
		view    func(*osView)
		mutate  func(*Launch)
		want    RecoveryOutcome
		reason  string
	}{
		{name: "supervisor permission refused", launch: &Launch{Supervisor: ProcessIdentity{PID: 424242, StartTicks: 7}},
			process: identity, view: func(v *osView) { v.stat = refuse(424242) }, want: RecoveryUnverifiable, reason: "supervisor_uninspectable"},
		{name: "program permission refused", launch: gone, process: identity,
			view: func(v *osView) { v.stat = refuse(pid) }, want: RecoveryUnverifiable, reason: "child_uninspectable"},
		{name: "boot id unreadable", launch: gone, process: identity,
			view: func(v *osView) { v.boot = func() (string, error) { return "", fs.ErrPermission } }, want: RecoveryUnverifiable, reason: "boot_id_unreadable"},
		{name: "working directory unreadable", launch: gone, process: identity,
			mutate: func(l *Launch) { l.WorkDir = "." },
			view:   func(v *osView) { v.cwdID = func(int) (FileIdentity, error) { return FileIdentity{}, fs.ErrPermission } },
			want:   RecoveryUnverifiable, reason: "child_working_directory_uninspectable"},
		{name: "launch record truncated", launch: gone, process: identity,
			mutate: func(l *Launch) { l.Store = "" }, want: RecoveryUnverifiable, reason: "launch_record_incomplete"},
		{name: "program already gone", launch: gone, process: gone.Supervisor, want: RecoveryAlreadyExited, reason: "child_gone"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, err := OpenStore(filepath.Join(t.TempDir(), "jobs"))
			if err != nil {
				t.Fatal(err)
			}
			id := abandonedJob(t, store, c.process, c.launch)
			if c.mutate != nil {
				j, _ := store.Load(id)
				c.mutate(j.Launch)
				if err := store.Save(j); err != nil {
					t.Fatal(err)
				}
			}
			view := hostView
			if c.view != nil {
				c.view(&view)
			}
			store.os = &view
			recovered, err := store.Recover(time.Now().UTC())
			if err != nil || len(recovered) != 1 {
				t.Fatalf("recovered %+v (%v)", recovered, err)
			}
			if r := recovered[0]; r.Outcome != c.want || r.Reason != c.reason || r.Signalled || !r.Interrupted {
				t.Fatalf("decided %+v, want %s (%s), interrupted, nothing signalled", r, c.want, c.reason)
			}
			after, _ := store.Load(id)
			if strings.Contains(after.Error, "were stopped") {
				t.Errorf("it claims a stop: %q", after.Error)
			}
			time.Sleep(50 * time.Millisecond)
			if !processGroupAlive(pid) {
				t.Fatal("an unverifiable record's process was signalled")
			}
		})
	}
}

// R13 — Cancel. Asked in the copy, it writes a marker in the COPY, which no
// executor watches: the original runs on. Asked of the original store — the
// CLI's `companion job cancel`, a Service nobody started — the original's own
// executor stops its own tree.
func TestR13OnlyTheOriginalsOwnCancellationStopsIt(t *testing.T) {
	s := startSupervisor(t)
	copyRoot := filepath.Join(t.TempDir(), "copied", "jobs")
	copyStore(t, s.jobs, copyRoot)
	copied, err := OpenStore(copyRoot)
	if err != nil {
		t.Fatal(err)
	}
	copyService, err := NewService(Options{Store: copied, Catalog: NewCatalog(t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := copyService.Cancel(s.id); err != nil {
		t.Fatalf("cancelling the copied record: %v", err)
	}
	time.Sleep(5 * cancelPollInterval)
	s.requireBuildAlive("cancelling the copied record")
	if _, err := os.Stat(filepath.Join(s.jobs, s.id, cancelName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("cancelling the copy reached the original's store: %v", err)
	}

	original, err := NewService(Options{Store: s.store(), Catalog: NewCatalog(t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := original.Cancel(s.id); err != nil {
		t.Fatal(err)
	}
	if line := s.nextLine(30 * time.Second); line != "done cancelled" {
		t.Fatalf("the original ended %q", line)
	}
	s.requireBuildGone("the original's own cancellation")
}

// R14 — terminal and queued records in a copy that carry the original's live
// process: the terminal one is never assessed and keeps its history; the
// queued one is interrupted locally; neither signals; deleting the copy's job
// and resolving its artifacts never reach the original store.
func TestR14TerminalAndQueuedCopiedRecordsNeverSignal(t *testing.T) {
	s := startSupervisor(t)
	copyRoot := filepath.Join(t.TempDir(), "copied", "jobs")
	copyStore(t, s.jobs, copyRoot)
	copied, err := OpenStore(copyRoot)
	if err != nil {
		t.Fatal(err)
	}
	source, err := copied.Load(s.id)
	if err != nil {
		t.Fatal(err)
	}
	originalArtifact := filepath.Join(layoutFor(s.jobs, s.id).Artifacts, "result.txt")
	if err := os.MkdirAll(filepath.Dir(originalArtifact), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(originalArtifact, []byte("the original's"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := func(state State) string {
		id, _ := NewID(time.Now())
		j := source.Clone()
		j.ID, j.State = id, state
		j.Artifacts = []Artifact{{Name: "result", Path: originalArtifact, Size: 14}}
		if err := copied.Save(j); err != nil {
			t.Fatal(err)
		}
		ageHeartbeat(t, copied, id)
		return id
	}
	terminal, queued := record(Succeeded), record(Queued)
	ageHeartbeat(t, copied, s.id)
	before, _ := os.ReadFile(filepath.Join(copyRoot, terminal, recordName))

	recovered, err := copied.Recover(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, found := decidedFor(recovered, terminal); found {
		t.Error("a terminal record was assessed")
	}
	if r, _ := decidedFor(recovered, queued); r.Outcome != RecoveryForeignStore || r.Signalled {
		t.Errorf("the queued copy decided %+v", r)
	}
	s.requireBuildAlive("recovering copied terminal and queued records")
	if after, _ := os.ReadFile(filepath.Join(copyRoot, terminal, recordName)); string(after) != string(before) {
		t.Error("recovery rewrote a terminal record")
	}
	if path, err := copied.ArtifactPath(terminal, "result"); err == nil {
		t.Errorf("a copied record's artifact resolved outside the copy, to %s", path)
	}
	if err := copied.Remove(terminal); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(originalArtifact); err != nil {
		t.Fatalf("removing the copy's job reached the original store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.jobs, s.id, recordName)); err != nil {
		t.Fatal(err)
	}
	s.finishOriginal()
}

// R15 — the program started and its record could not be written. The
// executor's own handle — never a pid read back — stops that tree, collects
// its exit, and the job fails saying why.
func TestR15AStartedJobThatCannotBeRecordedIsStoppedByItsOwnHandle(t *testing.T) {
	h := newHarness(t, fixtureProfile(t, "test.persist", barrierAction()))
	var mu sync.Mutex
	var started ProcessIdentity
	h.store.failSave = func(j *Job) error {
		mu.Lock()
		defer mu.Unlock()
		if j.State == Running && started.PID == 0 {
			started = j.Process
			return errors.New("the disk is full (fixture)")
		}
		return nil
	}
	finished := h.runToEnd(h.helperRequest("test.persist", "barrier"))
	mu.Lock()
	pid := started.PID
	mu.Unlock()
	if pid == 0 {
		t.Fatal("the fixture never saw the running save")
	}
	if finished.State != Failed || !strings.Contains(finished.Error, "the disk is full (fixture)") {
		t.Fatalf("state %s, error %q", finished.State, finished.Error)
	}
	deadline := time.Now().Add(5 * time.Second)
	for groupMembers(pid) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := groupMembers(pid); n != 0 {
		t.Fatalf("%d process(es) of the unrecorded job are still running", n)
	}
	if errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) == false {
		t.Error("the unrecorded job's process group still exists")
	}
}

// R16 — a store moved, or restored to its own path from a backup after the
// original was deleted, while the build the record names is still running and
// its supervisor is gone: refused, because a move or a restore is not proof.
func TestR16ARelocatedOrRestoredStoreIsNotPermission(t *testing.T) {
	t.Run("moved", func(t *testing.T) {
		s := startSupervisor(t)
		s.crash()
		moved := filepath.Join(s.root, "moved-jobs")
		if err := os.Rename(s.jobs, moved); err != nil {
			t.Fatal(err)
		}
		store, err := OpenStore(moved)
		if err != nil {
			t.Fatal(err)
		}
		ageHeartbeat(t, store, s.id)
		recovered, err := store.Recover(time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if r, _ := decidedFor(recovered, s.id); r.Outcome != RecoveryForeignStore || r.Reason != "store_path_differs" || r.Signalled {
			t.Fatalf("decided %+v", r)
		}
		s.requireBuildAlive("recovering a moved store")
	})
	t.Run("restored to the same path", func(t *testing.T) {
		s := startSupervisor(t)
		backup := filepath.Join(t.TempDir(), "backup")
		copyStore(t, s.jobs, backup)
		s.crash()
		if err := os.RemoveAll(s.jobs); err != nil {
			t.Fatal(err)
		}
		copyStore(t, backup, s.jobs)
		store := s.store()
		ageHeartbeat(t, store, s.id)
		recovered, err := store.Recover(time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		// Whichever fact gives it away first — a new inode, or (were inode
		// numbers reused) the program still sitting in the deleted original
		// directory — it is never an owned orphan.
		if r, _ := decidedFor(recovered, s.id); r.Outcome == RecoveryOwnedOrphanStopped || r.Signalled {
			t.Fatalf("decided %+v", r)
		} else {
			t.Logf("restored store decided %s (%s)", r.Outcome, r.Reason)
		}
		s.requireBuildAlive("recovering a store restored from a backup")
	})
	t.Run("restored to the same path, inode numbers reused", func(t *testing.T) {
		// The worst case of the one above, staged deterministically: the
		// filesystem handed the restored store and job directory the very
		// inode numbers the originals had. Only the kernel's own fact is left
		// — the program is still in the ORIGINAL (deleted) workspace — and it
		// alone must refuse.
		s := startSupervisor(t)
		launched, err := s.store().Load(s.id)
		if err != nil {
			t.Fatal(err)
		}
		backup := filepath.Join(t.TempDir(), "backup")
		copyStore(t, s.jobs, backup)
		s.crash()
		if err := os.RemoveAll(s.jobs); err != nil {
			t.Fatal(err)
		}
		copyStore(t, backup, s.jobs)
		store := s.store()
		ageHeartbeat(t, store, s.id)
		view := hostView
		view.fileID = func(path string) (FileIdentity, error) {
			switch path {
			case store.Root():
				return launched.Launch.StoreFile, nil
			case filepath.Join(store.Root(), s.id):
				return launched.Launch.JobFile, nil
			}
			return readFileIdentity(path)
		}
		store.os = &view
		recovered, err := store.Recover(time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if r, _ := decidedFor(recovered, s.id); r.Outcome != RecoveryUnverifiable || r.Reason != "child_working_directory_differs" || r.Signalled {
			t.Fatalf("decided %+v, want unverifiable (child_working_directory_differs)", r)
		}
		s.requireBuildAlive("recovering a restored store with reused inode numbers")
	})
}
