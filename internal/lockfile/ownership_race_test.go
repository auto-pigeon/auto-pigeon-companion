package lockfile

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// NEW_368 (post-push review): a lock's pathname is not its owner. Whoever
// judged a lock abandoned, and whoever once held one, acts on that lock only —
// never on the one that replaced it at the same path: not by breaking it, not
// by releasing it, not by refreshing it. Every interleaving is forced with
// barriers on the production paths, not left to timing.

const lockHelperEnv = "AUCOM_LOCKFILE_RACE_HELPER"

// staleRecord leaves a lock whose holder stopped on another machine and has
// not refreshed it for an hour.
func staleRecord(t *testing.T, guarded string) {
	t.Helper()
	writeRecord(t, guarded, Holder{SchemaVersion: SchemaVersion, PID: 1, Host: "a-machine-that-stopped",
		Program: "dead holder", Nonce: "dead", Since: time.Now().Add(-time.Hour).UTC()})
	ageLock(t, guarded, time.Hour)
}

func writeRecord(t *testing.T, guarded string, holder Holder) {
	t.Helper()
	raw, _ := json.Marshal(holder)
	if err := os.WriteFile(PathFor(guarded), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func ageLock(t *testing.T, guarded string, by time.Duration) {
	t.Helper()
	then := time.Now().Add(-by)
	if err := os.Chtimes(PathFor(guarded), then, then); err != nil {
		t.Fatal(err)
	}
}

// onDisk is the holder record at the lock's path, if one is there.
func onDisk(t *testing.T, guarded string) (Holder, bool) {
	t.Helper()
	raw, err := os.ReadFile(PathFor(guarded))
	if errors.Is(err, os.ErrNotExist) {
		return Holder{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var holder Holder
	_ = json.Unmarshal(raw, &holder)
	return holder, true
}

// pauseFirst stops the first call of a hook until resume is closed. Later
// calls pass.
func pauseFirst(t *testing.T, hook *func(string)) (paused, resume chan struct{}) {
	t.Helper()
	paused, resume = make(chan struct{}), make(chan struct{})
	var once sync.Once
	*hook = func(string) {
		first := false
		once.Do(func() { first = true })
		if first {
			close(paused)
			<-resume
		}
	}
	t.Cleanup(func() { *hook = nil })
	return paused, resume
}

func watchGuard(t *testing.T) chan struct{} {
	t.Helper()
	contended := make(chan struct{})
	var once sync.Once
	Hooks.GuardContended = func(string) { once.Do(func() { close(contended) }) }
	t.Cleanup(func() { Hooks.GuardContended = nil })
	return contended
}

type acquired struct {
	lock *Lock
	err  error
}

func acquireIn(guarded string, options Options) chan acquired {
	out := make(chan acquired, 1)
	go func() {
		lock, err := Acquire(guarded, options)
		out <- acquired{lock, err}
	}()
	return out
}

// awaitOrContend waits until an acquisition either finishes or has to wait
// for the guard another one holds.
func awaitOrContend(result chan acquired, contended chan struct{}) (acquired, bool) {
	select {
	case a := <-result:
		return a, true
	case <-contended:
		return acquired{}, false
	}
}

// exactlyOneHolds checks that one acquisition holds, the other was refused,
// and the record on disk is the holder's. It releases the holder.
func exactlyOneHolds(t *testing.T, guarded string, results ...acquired) {
	t.Helper()
	var holders []*Lock
	for _, r := range results {
		if r.err == nil {
			holders = append(holders, r.lock)
		} else if !errors.Is(r.err, ErrBusy) {
			t.Fatalf("an acquisition failed otherwise than busy: %v", r.err)
		}
	}
	if len(holders) != 1 {
		names := []string{}
		for _, h := range holders {
			names = append(names, h.Holder().Program)
		}
		t.Fatalf("%d holders at once (%v); want exactly one", len(holders), names)
	}
	record, ok := onDisk(t, guarded)
	if !ok || record.Nonce != holders[0].Holder().Nonce {
		t.Fatalf("%s holds the lock, but on disk: %+v (present %v)", holders[0].Holder().Program, record, ok)
	}
	if err := holders[0].Release(); err != nil {
		t.Fatalf("the holder's release: %v", err)
	}
}

// Two acquirers judge one lock abandoned. The second breaks it and holds its
// own; the first's delayed break must not remove it.
func TestAStaleBreakerNeverRemovesTheLockThatReplacedIt(t *testing.T) {
	path := guarded(t)
	staleRecord(t, path)
	paused, resume := pauseFirst(t, &Hooks.BeforeBreak)
	a := acquireIn(path, Options{Program: "breaker A", Timeout: 3 * time.Second})
	<-paused // A judged the dead holder's lock abandoned and is about to break it.
	contended := watchGuard(t)
	b := acquireIn(path, Options{Program: "breaker B", Timeout: 300 * time.Millisecond})
	bResult, bDone := awaitOrContend(b, contended)
	close(resume)
	aResult := <-a
	if !bDone {
		bResult = <-b
	}
	exactlyOneHolds(t, path, aResult, bResult)
}

// A holder descheduled between finding its own record and removing it must
// not remove the record of a holder that broke its lock meanwhile.
func TestAnOldHoldersReleaseNeverRemovesTheLockThatReplacedIt(t *testing.T) {
	path := guarded(t)
	first, err := Acquire(path, Options{Program: "first"})
	if err != nil {
		t.Fatal(err)
	}
	ageLock(t, path, 2*time.Hour) // first looks abandoned (asleep, say)
	paused, resume := pauseFirst(t, &Hooks.BeforeReleaseRemove)
	released := make(chan error, 1)
	go func() { released <- first.Release() }()
	<-paused
	contended := watchGuard(t)
	second := acquireIn(path, Options{Program: "second", Timeout: 3 * time.Second})
	result, done := awaitOrContend(second, contended)
	close(resume)
	<-released
	if !done {
		result = <-second
	}
	if result.err != nil {
		t.Fatalf("the second acquisition: %v", result.err)
	}
	record, ok := onDisk(t, path)
	if !ok || record.Nonce != result.lock.Holder().Nonce {
		t.Fatalf("an old holder's release removed the lock that replaced it: on disk %+v (present %v)", record, ok)
	}
	if err := result.lock.Release(); err != nil {
		t.Fatal(err)
	}
}

// An old holder's heartbeat, due when its lock was broken, must not refresh
// the replacement — which would keep a dead replacement looking alive.
func TestAnOldHeartbeatNeverRefreshesTheLockThatReplacedIt(t *testing.T) {
	path := guarded(t)
	// Installed before the heartbeat it stops is started.
	paused, resume := pauseFirst(t, &Hooks.BeforeRefresh)
	first, err := Acquire(path, Options{Program: "first", Stale: 40 * time.Millisecond, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	<-paused // first's heartbeat is due
	ageLock(t, path, 2*time.Hour)
	second, err := Acquire(path, Options{Program: "second", Timeout: time.Second})
	if err != nil {
		t.Fatalf("the replacement: %v", err)
	}
	defer second.Release()
	ageLock(t, path, 30*time.Second) // a known, still-live age for the replacement
	close(resume)
	// Release stops first's heartbeat and waits for it: the touch that was due
	// has happened, or has been refused, by the time it returns.
	if err := first.Release(); !errors.Is(err, ErrLost) {
		t.Fatalf("the old holder's release: %v; want ErrLost", err)
	}
	info, err := os.Stat(PathFor(path))
	if err != nil {
		t.Fatal(err)
	}
	if age := time.Since(info.ModTime()); age < 20*time.Second {
		t.Fatalf("the old holder's heartbeat refreshed the replacement (age now %v)", age)
	}
	if record, _ := onDisk(t, path); record.Nonce != second.Holder().Nonce {
		t.Fatalf("on disk: %+v", record)
	}
}

// Work done under a lock that was lost meanwhile is not reported as protected
// work. Nothing can undo what it wrote; the caller is told.
func TestWithReportsAProtectionLostDuringTheWork(t *testing.T) {
	path := guarded(t)
	err := With(path, Options{Program: "worker"}, func() error {
		writeRecord(t, path, Holder{SchemaVersion: SchemaVersion, PID: 2, Program: "somebody else", Nonce: "theirs"})
		return nil
	})
	if !errors.Is(err, ErrLost) {
		t.Fatalf("With after its lock was replaced: %v; want ErrLost", err)
	}
	if record, ok := onDisk(t, path); !ok || record.Nonce != "theirs" {
		t.Fatalf("the replacement's record: %+v (present %v)", record, ok)
	}
	// The body's own failure is still the body's.
	boom := errors.New("boom")
	if err := With(guarded(t), Options{}, func() error { return boom }); !errors.Is(err, boom) || errors.Is(err, ErrLost) {
		t.Fatalf("a failing body: %v", err)
	}
}

// The stale-breaker race between two processes: the helper judges a dead
// holder's lock abandoned and stops; this process then acquires; the helper
// resumes. Exactly one of them holds, and the record on disk is that one's.
func TestAnotherProcessesStaleBreakNeverRemovesThisProcessesLock(t *testing.T) {
	path := guarded(t)
	staleRecord(t, path)
	other := startLockHelper(t, "break", path)
	if line := other.line(t); line != "inspected" {
		t.Fatalf("helper: %q", line)
	}
	contended := watchGuard(t)
	result := acquireIn(path, Options{Program: "this process", Timeout: 500 * time.Millisecond})
	mine, done := awaitOrContend(result, contended)
	if _, err := other.stdin.Write([]byte("go\n")); err != nil {
		t.Fatal(err)
	}
	theirs := other.line(t)
	if !done {
		mine = <-result
	}
	if mine.err != nil && !errors.Is(mine.err, ErrBusy) {
		t.Fatal(mine.err)
	}
	if (mine.err == nil) == (theirs == "held") {
		t.Fatalf("this process holds the lock: %v; the other process says %q", mine.err == nil, theirs)
	}
	record, ok := onDisk(t, path)
	want := map[bool]string{true: "this process", false: "helper"}[mine.err == nil]
	if !ok || record.Program != want {
		t.Fatalf("%s holds the lock, but on disk: %+v (present %v)", want, record, ok)
	}
	other.stdin.Close()
	_ = other.command.Wait()
	if mine.err == nil {
		if err := mine.lock.Release(); err != nil {
			t.Fatal(err)
		}
	}
}

// A process killed holding the guard does not keep it: the operating system
// lets go, and an acquisition waiting for it proceeds. No pathname is ever
// judged for that.
func TestAProcessKilledHoldingTheGuardDoesNotKeepIt(t *testing.T) {
	path := guarded(t)
	other := startLockHelper(t, "guard", path)
	if line := other.line(t); line != "guarded" {
		t.Fatalf("helper: %q", line)
	}
	contended := watchGuard(t)
	result := acquireIn(path, Options{Program: "this process", Timeout: 5 * time.Second})
	if r, done := awaitOrContend(result, contended); done {
		t.Fatalf("an acquisition did not wait for the guard another process holds: %v", r.err)
	}
	_ = other.command.Process.Kill()
	mine := <-result
	if mine.err != nil {
		t.Fatalf("after the guard's holder was killed: %v", mine.err)
	}
	if err := mine.lock.Release(); err != nil {
		t.Fatal(err)
	}
}

// TestLockHelperProcess is the other process of the cross-process cases.
func TestLockHelperProcess(t *testing.T) {
	if os.Getenv(lockHelperEnv) == "" {
		t.Skip("run as a helper process only")
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	in := bufio.NewReader(os.Stdin)
	switch args[0] {
	case "guard":
		unlock, err := guard(PathFor(args[1]), time.Now().Add(time.Second))
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		defer unlock()
		fmt.Println("guarded")
		_, _ = in.ReadString('\n') // held until killed
	case "break":
		Hooks.BeforeBreak = func(string) {
			fmt.Println("inspected")
			_, _ = in.ReadString('\n')
		}
		lock, err := Acquire(args[1], Options{Program: "helper", Timeout: 3 * time.Second})
		if err != nil {
			fmt.Println("busy")
			os.Exit(0)
		}
		fmt.Println("held")
		_, _ = in.ReadString('\n') // until the test closes stdin
		if err := lock.Release(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Exit(0)
}

type lockHelper struct {
	command *exec.Cmd
	stdin   interface {
		Write([]byte) (int, error)
		Close() error
	}
	out *bufio.Reader
}

func startLockHelper(t *testing.T, args ...string) *lockHelper {
	t.Helper()
	command := exec.Command(os.Args[0], append([]string{"-test.run=^TestLockHelperProcess$", "-test.count=1", "--"}, args...)...)
	command.Env = append(os.Environ(), lockHelperEnv+"=1")
	stdin, _ := command.StdinPipe()
	stdout, _ := command.StdoutPipe()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	return &lockHelper{command: command, stdin: stdin, out: bufio.NewReader(stdout)}
}

func (h *lockHelper) line(t *testing.T) string {
	t.Helper()
	line, err := h.out.ReadString('\n')
	if err != nil && line == "" {
		t.Fatalf("the helper process said nothing: %v", err)
	}
	return strings.TrimSpace(line)
}
