package tooluse

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// NEW_368 (gate ownership): a gate's pathname is not its owner. Whoever
// judged a gate dead, and whoever once held a gate, acts on that gate only —
// never on the one that has replaced it at the same path. Every interleaving
// here is forced with barriers on the production paths, not left to timing.

const gateHelperEnv = "AUCOM_TOOLUSE_GATE_HELPER"

func open(t *testing.T, root string) *Registry {
	t.Helper()
	registry, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

// deadGate leaves a gate whose holder stopped: written on another machine,
// so its liveness is its heartbeat, and not refreshed for two stale periods.
// The same on every platform.
func deadGate(t *testing.T, r *Registry, tool string) {
	t.Helper()
	record := Record{Schema: Schema, Kind: "switch", Tools: []string{tool}, Program: "dead switcher",
		Host: "a-machine-that-stopped", PID: 1, Since: time.Now().Add(-time.Hour).UTC()}
	if err := writeRecord(r.gatePath(tool), record, true); err != nil {
		t.Fatal(err)
	}
	age(t, r.gatePath(tool), 2*staleAfter)
}

func age(t *testing.T, path string, by time.Duration) {
	t.Helper()
	then := time.Now().Add(-by)
	if err := os.Chtimes(path, then, then); err != nil {
		t.Fatal(err)
	}
}

// gateOn is the gate record at tool's pathname, if one is there.
func gateOn(t *testing.T, r *Registry, tool string) (Record, bool) {
	t.Helper()
	record, _, err := readRecord(r.gatePath(tool))
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return record, true
}

// pauseFirstReclaim stops the first reclamation of a gate judged dead at the
// moment between the judgement and the removal, until resume is closed.
// Later reclamations pass.
func pauseFirstReclaim(t *testing.T) (paused, resume chan struct{}) {
	t.Helper()
	paused, resume = make(chan struct{}), make(chan struct{})
	var once sync.Once
	beforeReclaim = func(string) {
		first := false
		once.Do(func() { first = true })
		if first {
			close(paused)
			<-resume
		}
	}
	t.Cleanup(func() { beforeReclaim = nil })
	return paused, resume
}

// watchGuard reports the first time an acquisition has to wait for the
// tools folder's coordination guard.
func watchGuard(t *testing.T) chan struct{} {
	t.Helper()
	contended := make(chan struct{})
	var once sync.Once
	guardContended = func() { once.Do(func() { close(contended) }) }
	t.Cleanup(func() { guardContended = nil })
	return contended
}

type switched struct {
	gate *Gate
	err  error
}

func switchIn(r *Registry, tool, program string) chan switched {
	out := make(chan switched, 1)
	go func() {
		gate, err := r.Switch(tool, program)
		out <- switched{gate, err}
	}()
	return out
}

// awaitOrContend: the old protocol lets the second party run to completion
// while the first is paused; a sound one makes it wait for the first. Either
// way the test goes on deterministically.
func awaitOrContend(result chan switched, contended chan struct{}) (switched, bool) {
	select {
	case s := <-result:
		return s, true
	case <-contended:
		return switched{}, false
	}
}

// Two processes judge one gate dead. The first reclaims it and a new holder
// publishes its own; the second's delayed reclamation must not remove it —
// through Switch, and through a lease's check (switching).
func TestAStaleReclaimerNeverRemovesTheGateThatReplacedIt(t *testing.T) {
	t.Run("both reclaim through Switch", func(t *testing.T) {
		root := t.TempDir()
		a, b := open(t, root), open(t, root)
		deadGate(t, a, "ericw-tools")
		paused, resume := pauseFirstReclaim(t)
		contended := watchGuard(t)
		bResult := switchIn(b, "ericw-tools", "switcher B")
		<-paused // B judged the gate dead and is about to remove it
		aResult := switchIn(a, "ericw-tools", "switcher A")
		aSwitched, aDone := awaitOrContend(aResult, contended)
		close(resume)
		bSwitched := <-bResult
		if !aDone {
			aSwitched = <-aResult
		}
		var holders []*Gate
		for _, s := range []switched{aSwitched, bSwitched} {
			switch {
			case s.err == nil:
				holders = append(holders, s.gate)
			case !errors.Is(s.err, ErrSwitchBusy):
				t.Fatalf("a switcher failed: %v", s.err)
			}
		}
		if len(holders) != 1 {
			t.Fatalf("%d switchers believe they hold one toolchain's gate", len(holders))
		}
		owner, ok := gateOn(t, a, "ericw-tools")
		if !ok {
			t.Fatal("the switcher that holds the gate has no gate on disk")
		}
		if holder := map[bool]string{true: "switcher A", false: "switcher B"}[aSwitched.err == nil]; owner.Program != holder {
			t.Fatalf("the gate on disk is %q's, but %q holds it", owner.Program, holder)
		}
		if _, err := open(t, root).Use([]string{"ericw-tools"}, "new build", 0); !errors.Is(err, ErrSwitching) {
			t.Fatalf("a new build entered while the gate is held: %v", err)
		}
		holders[0].Release()
		if _, ok := gateOn(t, a, "ericw-tools"); ok {
			t.Fatal("the holder's release left its gate")
		}
	})
	t.Run("a lease's check reclaims, a switcher publishes", func(t *testing.T) {
		root := t.TempDir()
		a, b := open(t, root), open(t, root)
		deadGate(t, a, "ericw-tools")
		paused, resume := pauseFirstReclaim(t)
		contended := watchGuard(t)
		type used struct {
			lease *Lease
			err   error
		}
		bResult := make(chan used, 1)
		go func() {
			lease, err := b.Use([]string{"companion", "ericw-tools"}, "build B", 0)
			bResult <- used{lease, err}
		}()
		<-paused // B's check judged the gate dead and is about to remove it
		aResult := switchIn(a, "ericw-tools", "switcher A")
		aSwitched, aDone := awaitOrContend(aResult, contended)
		close(resume)
		bUsed := <-bResult
		if !aDone {
			aSwitched = <-aResult
		}
		if aSwitched.err != nil {
			t.Fatalf("the switcher was refused a dead gate: %v", aSwitched.err)
		}
		defer aSwitched.gate.Release()
		if owner, ok := gateOn(t, a, "ericw-tools"); !ok || owner.Program != "switcher A" {
			t.Fatalf("switcher A holds the gate, but on disk: %+v (present %v)", owner, ok)
		}
		// B either saw the gate and withdrew, or was admitted and is seen.
		switch {
		case bUsed.err == nil:
			if len(aSwitched.gate.Users()) == 0 {
				t.Fatal("a build was admitted that the switch does not wait for")
			}
			bUsed.lease.Release()
		case !errors.Is(bUsed.err, ErrSwitching):
			t.Fatal(bUsed.err)
		}
		c := open(t, root)
		if _, err := c.Use([]string{"ericw-tools"}, "build C", 0); !errors.Is(err, ErrSwitching) {
			t.Fatalf("a new build entered while switcher A holds the gate: %v", err)
		}
		if _, err := c.Switch("ericw-tools", "switcher C"); !errors.Is(err, ErrSwitchBusy) {
			t.Fatalf("a second switcher entered while switcher A holds the gate: %v", err)
		}
	})
}

// A holder whose gate was reclaimed (it stopped heartbeating) and that then
// wakes: its heartbeat and its release leave the replacement's gate alone.
func TestAnOldHoldersReleaseAndHeartbeatNeverTouchTheReplacement(t *testing.T) {
	root := t.TempDir()
	old := open(t, root)
	old.host = "a-machine-that-stopped" // its liveness is its heartbeat
	oldGate, err := old.Switch("ericw-tools", "old holder")
	if err != nil {
		t.Fatal(err)
	}
	path := old.gatePath("ericw-tools")
	age(t, path, 2*staleAfter)
	c := open(t, root)
	replacement, err := c.Switch("ericw-tools", "replacement")
	if err != nil {
		t.Fatalf("the stopped holder's gate was not recovered: %v", err)
	}
	defer replacement.Release()
	age(t, path, staleAfter/2)
	before, _ := os.Stat(path)

	oldGate.beat()
	if after, _ := os.Stat(path); !after.ModTime().Equal(before.ModTime()) {
		t.Error("the old holder's heartbeat refreshed the replacement's gate")
	}
	if oldGate.Held() || !replacement.Held() {
		t.Fatalf("held: old %v, replacement %v", oldGate.Held(), replacement.Held())
	}
	oldGate.Release()
	if owner, ok := gateOn(t, c, "ericw-tools"); !ok || owner.Program != "replacement" {
		t.Fatalf("after the old holder's release the gate is %+v (present %v)", owner, ok)
	}
	if _, err := c.Use([]string{"ericw-tools"}, "new build", 0); !errors.Is(err, ErrSwitching) {
		t.Fatalf("a new build entered while the replacement holds the gate: %v", err)
	}
	replacement.beat()
	if after, _ := os.Stat(path); !after.ModTime().After(before.ModTime()) {
		t.Fatal("the replacement's own heartbeat did not refresh its gate")
	}
	replacement.Release()
	if _, ok := gateOn(t, c, "ericw-tools"); ok {
		t.Fatal("the replacement's release left its gate")
	}
}

// A gate that cannot be read may be one being written or one somebody else
// is about to finish: it holds, and it is not removed — until it has gone a
// whole stale period without a heartbeat, when it is recovered.
func TestAnUnreadableGateHoldsUntilItIsStale(t *testing.T) {
	root := t.TempDir()
	r := open(t, root)
	path := r.gatePath("ericw-tools")
	if err := os.WriteFile(path, []byte(`{"schema":"aucom.tool-use/1","ki`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Switch("ericw-tools", "switcher"); !errors.Is(err, ErrSwitchBusy) {
		t.Fatalf("a switcher took an unreadable, fresh gate: %v", err)
	}
	if _, err := r.Use([]string{"ericw-tools"}, "build", 0); !errors.Is(err, ErrSwitching) {
		t.Fatalf("a build entered past an unreadable, fresh gate: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("an unreadable, fresh gate was removed: %v", err)
	}
	age(t, path, 2*staleAfter)
	gate, err := r.Switch("ericw-tools", "switcher")
	if err != nil {
		t.Fatalf("an unreadable gate abandoned for two stale periods was not recovered: %v", err)
	}
	gate.Release()
}

// Two switchers released at one instant on an empty gate, and on a dead
// one: exactly one holds it, and the other is refused retryably.
func TestTwoSwitchersAtOnceOneHoldsTheGate(t *testing.T) {
	for _, dead := range []bool{false, true} {
		root := t.TempDir()
		a, b := open(t, root), open(t, root)
		if dead {
			deadGate(t, a, "ericw-tools")
		}
		start := make(chan struct{})
		results := make(chan switched, 2)
		for _, r := range []*Registry{a, b} {
			go func(r *Registry) {
				<-start
				gate, err := r.Switch("ericw-tools", "switcher")
				results <- switched{gate, err}
			}(r)
		}
		close(start)
		var held []*Gate
		for range 2 {
			s := <-results
			switch {
			case s.err == nil:
				held = append(held, s.gate)
			case !errors.Is(s.err, ErrSwitchBusy):
				t.Fatal(s.err)
			}
		}
		if len(held) != 1 {
			t.Fatalf("dead gate %v: %d switchers hold the gate", dead, len(held))
		}
		held[0].Release()
	}
}

// --- another process ------------------------------------------------------------

// TestGateHelperProcess is another Companion process.
//
//	reclaim <root> <tool>: Switch; the first reclamation of a gate judged
//	  dead stops after the judgement, says "inspected", and waits for a line
//	  on stdin. Then "gate" or "busy", and a held gate is released when stdin
//	  closes.
//	guard <root>: takes the guard, says "guarded", and keeps it until it is
//	  killed (or stdin closes).
func TestGateHelperProcess(t *testing.T) {
	if os.Getenv(gateHelperEnv) == "" {
		t.Skip("only a child process runs this")
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	registry, err := Open(args[1])
	if err != nil {
		os.Exit(3)
	}
	in := bufio.NewReader(os.Stdin)
	switch args[0] {
	case "reclaim":
		var once sync.Once
		beforeReclaim = func(string) {
			once.Do(func() {
				os.Stdout.WriteString("inspected\n")
				_, _ = in.ReadString('\n')
			})
		}
		gate, err := registry.Switch(args[2], "helper switcher")
		if err != nil {
			os.Stdout.WriteString("busy\n")
			os.Exit(0)
		}
		os.Stdout.WriteString("gate\n")
		_, _ = io.Copy(io.Discard, in)
		gate.Release()
		os.Exit(0)
	case "guard":
		if _, err := registry.guard(); err != nil {
			os.Exit(4)
		}
		os.Stdout.WriteString("guarded\n")
		_, _ = io.Copy(io.Discard, in)
		os.Exit(0)
	}
	os.Exit(2)
}

type gateHelper struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	out     *bufio.Reader
}

func startGateHelper(t *testing.T, args ...string) *gateHelper {
	t.Helper()
	command := exec.Command(os.Args[0], append([]string{"-test.run=^TestGateHelperProcess$", "-test.count=1", "--"}, args...)...)
	command.Env = append(os.Environ(), gateHelperEnv+"=1")
	stdin, _ := command.StdinPipe()
	stdout, _ := command.StdoutPipe()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	return &gateHelper{command: command, stdin: stdin, out: bufio.NewReader(stdout)}
}

func (h *gateHelper) line(t *testing.T) string {
	t.Helper()
	line, err := h.out.ReadString('\n')
	if err != nil && line == "" {
		t.Fatalf("the helper process said nothing: %v", err)
	}
	return strings.TrimSpace(line)
}

// The stale-reclaimer race between two processes: the helper judges a dead
// gate dead and stops; this process then switches; the helper resumes.
// Exactly one of them holds the gate, and the gate on disk is that one's.
func TestAnotherProcessesStaleReclamationNeverRemovesThisProcessesGate(t *testing.T) {
	root := t.TempDir()
	r := open(t, root)
	deadGate(t, r, "ericw-tools")
	other := startGateHelper(t, "reclaim", root, "ericw-tools")
	if line := other.line(t); line != "inspected" {
		t.Fatalf("helper: %q", line)
	}
	contended := watchGuard(t)
	result := switchIn(r, "ericw-tools", "this process")
	mine, done := awaitOrContend(result, contended)
	if _, err := other.stdin.Write([]byte("go\n")); err != nil {
		t.Fatal(err)
	}
	theirs := other.line(t)
	if !done {
		mine = <-result
	}
	if mine.err != nil && !errors.Is(mine.err, ErrSwitchBusy) {
		t.Fatal(mine.err)
	}
	if (mine.err == nil) == (theirs == "gate") {
		t.Fatalf("this process holds the gate: %v; the other process says %q", mine.err == nil, theirs)
	}
	owner, ok := gateOn(t, r, "ericw-tools")
	want := map[bool]string{true: "this process", false: "helper switcher"}[mine.err == nil]
	if !ok || owner.Program != want {
		t.Fatalf("%s holds the gate, but on disk: %+v (present %v)", want, owner, ok)
	}
	other.stdin.Close()
	_ = other.command.Wait()
	if mine.err == nil {
		mine.gate.Release()
	}
	if _, ok := gateOn(t, r, "ericw-tools"); ok {
		t.Fatal("a gate outlived both holders' releases")
	}
}

// A process killed holding the guard does not keep it: the operating system
// lets go, and a switch waiting for it proceeds. Leases never need the guard
// while no gate is there to reclaim.
func TestAProcessKilledHoldingTheGuardDoesNotKeepIt(t *testing.T) {
	root := t.TempDir()
	r := open(t, root)
	deadGate(t, r, "ericw-tools")
	other := startGateHelper(t, "guard", root)
	if line := other.line(t); line != "guarded" {
		t.Fatalf("helper: %q", line)
	}
	lease, err := r.Use([]string{"q3map2"}, "a build of another toolchain", 0)
	if err != nil {
		t.Fatalf("a lease with no gate to reclaim waited for the guard: %v", err)
	}
	lease.Release()
	contended := watchGuard(t)
	result := switchIn(r, "ericw-tools", "switcher")
	if s, done := awaitOrContend(result, contended); done {
		t.Fatalf("a switch did not wait for the guard another process holds: %v", s.err)
	}
	_ = other.command.Process.Kill()
	_ = other.command.Wait()
	s := <-result
	if s.err != nil {
		t.Fatalf("the guard of a killed process was not let go: %v", s.err)
	}
	if owner, ok := gateOn(t, r, "ericw-tools"); !ok || owner.Program != "switcher" || owner.ID == "" {
		t.Fatalf("the gate on disk: %+v (present %v)", owner, ok)
	}
	s.gate.Release()
}

// A switch that is refused, and one whose gate cannot be written, leave
// nothing behind and do not keep the guard: the next switch proceeds at once.
func TestARefusedOrFailedSwitchLeavesNothingAndFreesTheGuard(t *testing.T) {
	root := t.TempDir()
	r := open(t, root)
	gate, err := r.Switch("ericw-tools", "holder")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Switch("ericw-tools", "second"); !errors.Is(err, ErrSwitchBusy) {
		t.Fatalf("a second switcher: %v", err)
	}
	if _, err := r.Use([]string{"ericw-tools"}, "build", 0); !errors.Is(err, ErrSwitching) {
		t.Fatalf("a build during the switch: %v", err)
	}
	entries, _ := os.ReadDir(r.dir)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, ",") != "guard.lock,switch-ericw-tools.json" {
		t.Fatalf("refusals left: %v", names)
	}
	gate.Release()
	gate.Release()
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		if err := os.Chmod(r.dir, 0o500); err != nil {
			t.Fatal(err)
		}
		_, err := r.Switch("ericw-tools", "cannot write")
		_ = os.Chmod(r.dir, 0o700)
		if err == nil || errors.Is(err, ErrSwitchBusy) {
			t.Fatalf("a switch whose gate cannot be written: %v", err)
		}
	}
	contended := watchGuard(t)
	again, err := r.Switch("ericw-tools", "next")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-contended:
		t.Fatal("a refused or failed switch kept the guard")
	default:
	}
	again.Release()
	if _, ok := gateOn(t, r, "ericw-tools"); ok {
		t.Fatal("a released gate is still there")
	}
}
