//go:build unix

package tooluse

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// 01_NEW_368 §2: the use/switch protocol of one tools folder.

const helperEnv = "AUCOM_TOOLUSE_HELPER"

// TestHelperProcess is another Companion process: it takes a lease on the
// tools folder (argv after --) and holds it until stdin closes; with
// "orphan" it first records a job store and a running job whose program — a
// separate, session-detached sleeper — outlives it.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) == "" {
		t.Skip("only a child process runs this")
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	registry, err := Open(args[0])
	if err != nil {
		os.Exit(3)
	}
	lease, err := registry.Use(strings.Split(args[1], ","), "helper", time.Second)
	if err != nil {
		os.Stdout.WriteString("refused\n")
		os.Exit(4)
	}
	if len(args) > 3 && args[2] == "orphan" {
		store, err := job.OpenStore(args[3])
		if err != nil {
			os.Exit(5)
		}
		_ = lease.Pin([]string{args[4]})
		_ = lease.AddStore(store.Root())
		sleeper := exec.Command("sleep", "300")
		sleeper.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := sleeper.Start(); err != nil {
			os.Exit(6)
		}
		id, _ := job.NewID(time.Now())
		self := job.ProcessIdentity{PID: os.Getpid(), StartTicks: job.ProcessStartTicks(os.Getpid())}
		j := &job.Job{SchemaVersion: job.SchemaVersion, ID: id, State: job.Running, ProfileID: "auto-pigeon.ericw-tools.q1",
			ActionID: "vis", CreatedAt: time.Now(), Owner: job.Owner{PID: self.PID},
			Process: job.ProcessIdentity{PID: sleeper.Process.Pid, StartTicks: job.ProcessStartTicks(sleeper.Process.Pid)},
			Launch:  &job.Launch{Supervisor: self}}
		if err := store.Save(j); err != nil {
			os.Exit(7)
		}
		os.Stdout.WriteString("sleeper " + strconv.Itoa(sleeper.Process.Pid) + "\n")
	}
	os.Stdout.WriteString("held\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
	lease.Release()
	os.Exit(0)
}

type helper struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	lines   []string
}

func startHelper(t *testing.T, args ...string) *helper {
	t.Helper()
	command := exec.Command(os.Args[0], append([]string{"-test.run=^TestHelperProcess$", "-test.count=1", "--"}, args...)...)
	command.Env = append(os.Environ(), helperEnv+"=1")
	stdin, _ := command.StdinPipe()
	stdout, _ := command.StdoutPipe()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	h := &helper{command: command, stdin: stdin}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			h.lines = append(h.lines, line)
		}
		if line == "held" || line == "refused" || err != nil {
			break
		}
	}
	return h
}

func (h *helper) held() bool { return len(h.lines) > 0 && h.lines[len(h.lines)-1] == "held" }

func (h *helper) finish(t *testing.T) {
	t.Helper()
	h.stdin.Close()
	_ = h.command.Wait()
}

// Another process's lease holds a switch; its release frees it; while the
// switch waits, a new lease is refused retryably and leaves nothing behind.
func TestAnotherProcessesLeaseHoldsASwitchAndTheSwitchHoldsNewLeasesOut(t *testing.T) {
	root := t.TempDir()
	registry := open(t, root)
	other := startHelper(t, root, "companion,ericw-tools")
	if !other.held() {
		t.Fatalf("helper: %v", other.lines)
	}
	gate, err := registry.Switch("ericw-tools", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	users := gate.Users()
	if len(users) != 1 || users[0].Here || !strings.Contains(users[0].Label, "helper") {
		t.Fatalf("users %+v", users)
	}
	// A q3map2 build is not held by an EricW switch.
	q3, err := registry.Use([]string{"companion", "q3map2"}, "q3 build", 0)
	if err != nil {
		t.Fatalf("an unrelated toolchain was held: %v", err)
	}
	q3.Release()
	// An EricW build is, retryably, and leaves no lease file.
	if _, err := registry.Use([]string{"companion", "ericw-tools"}, "new build", 150*time.Millisecond); !errors.Is(err, ErrSwitching) {
		t.Fatalf("a new lease during a switch: %v", err)
	}
	if leases := registry.leases(); len(leases) != 1 {
		t.Fatalf("a refused lease left a record: %d leases", len(leases))
	}
	// A second switcher of the same toolchain is refused; another process
	// trying to lease while the gate is held is refused too.
	if _, err := registry.Switch("ericw-tools", "second switcher"); !errors.Is(err, ErrSwitchBusy) {
		t.Fatalf("a second switcher: %v", err)
	}
	if blocked := startHelper(t, root, "companion,ericw-tools"); blocked.held() {
		t.Fatal("another process got a lease while the switch was held")
	}
	other.finish(t)
	if users := gate.Users(); len(users) != 0 {
		t.Fatalf("after the other build ended: %+v", users)
	}
	gate.Release()
	lease, err := registry.Use([]string{"companion", "ericw-tools"}, "after the switch", 0)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}

// The race, both ways round, with a barrier between writing one's own record
// and reading the other's: no lease is admitted that the switch does not see.
func TestAdmissionAndSwitchNeverBothProceed(t *testing.T) {
	t.Run("lease written, then the switch reads", func(t *testing.T) {
		registry := open(t, t.TempDir())
		written, proceed := make(chan struct{}), make(chan struct{})
		afterLeaseWritten = func() { close(written); <-proceed }
		defer func() { afterLeaseWritten = nil }()
		result := make(chan error, 1)
		var lease *Lease
		go func() {
			var err error
			lease, err = registry.Use([]string{"ericw-tools"}, "racing build", 0)
			result <- err
		}()
		<-written
		gate, err := registry.Switch("ericw-tools", "switch")
		if err != nil {
			t.Fatal(err)
		}
		users := gate.Users()
		close(proceed)
		err = <-result
		// Either the switch saw the lease, or the lease saw the gate and
		// withdrew — never neither.
		switch {
		case err == nil && len(users) == 0:
			t.Fatal("a lease was admitted that the switch did not see")
		case err == nil:
			lease.Release()
		case !errors.Is(err, ErrSwitching):
			t.Fatal(err)
		}
		gate.Release()
	})
	t.Run("gate written, then the lease reads", func(t *testing.T) {
		registry := open(t, t.TempDir())
		written, proceed := make(chan struct{}), make(chan struct{})
		afterGateWritten = func() { close(written); <-proceed }
		defer func() { afterGateWritten = nil }()
		gated := make(chan *Gate, 1)
		go func() {
			gate, err := registry.Switch("ericw-tools", "switch")
			if err != nil {
				t.Error(err)
			}
			gated <- gate
		}()
		<-written
		_, err := registry.Use([]string{"ericw-tools"}, "racing build", 0)
		close(proceed)
		gate := <-gated
		defer gate.Release()
		if !errors.Is(err, ErrSwitching) {
			t.Fatalf("a lease was admitted after the gate was written: %v", err)
		}
		if users := gate.Users(); len(users) != 0 {
			t.Fatalf("the refused lease is still seen: %+v", users)
		}
	})
}

// Two spellings of one tools folder are one folder; two folders are two.
func TestOneToolsFolderIsOneProtectionWhateverItIsCalled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	base := t.TempDir()
	root := filepath.Join(base, "tools")
	alias := filepath.Join(base, "alias")
	a := open(t, root)
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	b := open(t, alias)
	lease, err := b.Use([]string{"ericw-tools"}, "config B's build", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	gate, err := a.Switch("ericw-tools", "config A's update")
	if err != nil {
		t.Fatal(err)
	}
	if len(gate.Users()) != 1 {
		t.Fatal("a lease through another spelling of the folder is not seen")
	}
	gate.Release()
	unrelated := open(t, filepath.Join(base, "other-tools"))
	other, err := unrelated.Switch("ericw-tools", "an unrelated folder's update")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	if len(other.Users()) != 0 {
		t.Fatal("an unrelated tools folder sees this one's lease")
	}

	// The guard is the folder's too: held through one spelling, a
	// reclamation through the other waits for it; an unrelated folder's
	// switch and another toolchain's lease do not.
	deadGate(t, a, "ericw-tools")
	unlock, err := b.guard()
	if err != nil {
		t.Fatal(err)
	}
	contended := watchGuard(t)
	result := switchIn(a, "ericw-tools", "config A's update")
	if s, done := awaitOrContend(result, contended); done {
		unlock()
		t.Fatalf("the guard taken through another spelling did not hold a reclamation: %v", s.err)
	}
	q3, err := a.Use([]string{"q3map2"}, "a q3map2 build", 0)
	if err != nil {
		t.Fatalf("another toolchain's lease waited: %v", err)
	}
	q3.Release()
	q3Unrelated, err := unrelated.Switch("q3map2", "an unrelated folder's q3map2 update")
	if err != nil {
		t.Fatalf("an unrelated folder's switch waited for this folder's guard: %v", err)
	}
	q3Unrelated.Release()
	unlock()
	if s := <-result; s.err != nil {
		t.Fatal(s.err)
	} else {
		s.gate.Release()
	}
}

// A parent killed while its compiler runs on: the switch does not wait for
// the orphan (its bindings are not the orphan's concern), but removal of the
// installation it pinned does, by the job's own verified process identity,
// until the orphan ends. Nothing kills it.
func TestAnOrphanedCompilerKeepsItsInstallationUntilItEnds(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process identity is read from /proc")
	}
	root := t.TempDir()
	installation := filepath.Join(root, "ericw-tools", "1.4536")
	registry := open(t, root)
	parent := startHelper(t, root, "companion,ericw-tools", "orphan", filepath.Join(t.TempDir(), "jobs"), installation)
	if !parent.held() || len(parent.lines) < 2 {
		t.Fatalf("helper: %v", parent.lines)
	}
	var sleeper int
	for _, line := range parent.lines {
		if rest, ok := strings.CutPrefix(line, "sleeper "); ok {
			sleeper, _ = strconv.Atoi(rest)
		}
	}
	defer syscall.Kill(sleeper, syscall.SIGKILL)
	if refs := registry.References("ericw-tools", installation); len(refs) != 1 || !strings.Contains(refs[0], "a build in") {
		t.Fatalf("while the parent lives: %v", refs)
	}
	_ = parent.command.Process.Kill()
	_ = parent.command.Wait()
	gate, err := registry.Switch("ericw-tools", "update")
	if err != nil {
		t.Fatal(err)
	}
	if users := gate.Users(); len(users) != 0 {
		t.Fatalf("a dead parent's lease holds the switch: %+v", users)
	}
	gate.Release()
	refs := registry.References("ericw-tools", installation)
	if len(refs) != 1 || !strings.Contains(refs[0], "still running") {
		t.Fatalf("the orphan's installation is not kept: %v", refs)
	}
	if err := syscall.Kill(sleeper, 0); err != nil {
		t.Fatalf("the orphan was signalled: %v", err)
	}
	if refs := registry.References("ericw-tools", filepath.Join(root, "ericw-tools", "1.4540")); len(refs) != 0 {
		t.Fatalf("an installation the orphan did not pin is kept: %v", refs)
	}
	_ = syscall.Kill(sleeper, syscall.SIGKILL)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if running, _ := job.ProcessRunning(job.ProcessIdentity{PID: sleeper, StartTicks: job.ProcessStartTicks(sleeper)}); !running {
			break
		}
	}
	if refs := registry.References("ericw-tools", installation); len(refs) != 0 {
		t.Fatalf("after the orphan ended: %v", refs)
	}
	if leases := registry.leases(); len(leases) != 0 {
		t.Fatal("the dead lease was not cleared once it was verifiably over")
	}
}

// A switch gate whose holder died does not block anything.
func TestADeadSwitchersGateIsBroken(t *testing.T) {
	root := t.TempDir()
	registry := open(t, root)
	dead := exec.Command(os.Args[0], "-test.run=^$")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	record := Record{Schema: Schema, Kind: "switch", Tools: []string{"ericw-tools"}, Host: registry.host,
		PID: dead.Process.Pid, StartTicks: 1, Since: time.Now()}
	if err := writeRecord(registry.gatePath("ericw-tools"), record, true); err != nil {
		t.Fatal(err)
	}
	lease, err := registry.Use([]string{"ericw-tools"}, "build", 0)
	if err != nil {
		t.Fatalf("a dead switcher's gate held a lease out: %v", err)
	}
	lease.Release()
}
