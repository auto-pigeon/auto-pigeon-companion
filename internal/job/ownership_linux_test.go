//go:build linux

package job

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A copied job store must never stop the original's build (NEW_326).
//
// Q3_017E copied the state of a Companion that was 44 minutes into a q3map2
// LIGHT, started a second Companion on the copy, and that one's start-up
// recovery SIGKILLed the original compiler: the copied record named a pid and
// a start time, the kernel still had exactly that process, and that was all
// recovery asked. Identity is not ownership. Every fixture below runs a real
// supervisor in a process of its own (helper_supervise_test.go) and a real
// compiler stand-in held at a barrier, and asserts on the processes
// themselves — never on whether a kill function was called.

// supervised is one supervisor process and the job it is running.
type supervised struct {
	t      *testing.T
	root   string
	jobs   string
	id     string
	cmd    *exec.Cmd
	lines  chan string
	leader int
	child  int
	// Start ticks taken when the barrier said ready, so cleanup signals only a
	// process that is still the one this test started.
	leaderTicks, childTicks uint64
}

func barrierAction() fixtureAction {
	return modeAction("barrier", "barrier")
}

// startSupervisor runs a Companion in a child process, has it start the
// barrier job, and returns once the job's record says Running with its
// process recorded and both barrier processes exist.
func startSupervisor(t *testing.T) *supervised {
	t.Helper()
	root := t.TempDir()
	writeProfile(t, filepath.Join(root, "profiles"), fixtureProfile(t, "test.recovery", barrierAction()))
	command := exec.Command(helperPath(t), helperFlag, "supervise", root, "test.recovery", "barrier")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = &testLogWriter{t: t, prefix: "supervisor stderr: "}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	s := &supervised{t: t, root: root, jobs: filepath.Join(root, "jobs"), cmd: command, lines: make(chan string, 16)}
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			s.lines <- scanner.Text()
		}
		close(s.lines)
	}()
	t.Cleanup(s.cleanup)

	line := s.nextLine(30 * time.Second)
	if !strings.HasPrefix(line, "job ") {
		t.Fatalf("the supervisor said %q, want its job id", line)
	}
	s.id = strings.TrimPrefix(line, "job ")

	ready := filepath.Join(s.workspace(), "ready")
	deadline := time.Now().Add(30 * time.Second)
	for {
		raw, err := os.ReadFile(ready)
		record, loadErr := s.store().Load(s.id)
		if err == nil && loadErr == nil && record.State == Running && record.Process.PID != 0 {
			fields := strings.Fields(string(raw))
			s.leader, _ = strconv.Atoi(fields[0])
			s.child, _ = strconv.Atoi(fields[1])
			s.leaderTicks, s.childTicks = processStartTicks(s.leader), processStartTicks(s.child)
			if record.Process.PID != s.leader {
				t.Fatalf("the record names pid %d, the barrier's leader is %d", record.Process.PID, s.leader)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the barrier job never became ready (ready: %v, record: %v)", err, loadErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return s
}

func (s *supervised) store() *Store {
	store, err := OpenStore(s.jobs)
	if err != nil {
		s.t.Fatal(err)
	}
	return store
}

func (s *supervised) workspace() string { return layoutFor(s.jobs, s.id).Workspace }

func (s *supervised) nextLine(patience time.Duration) string {
	s.t.Helper()
	select {
	case line, open := <-s.lines:
		if !open {
			s.t.Fatal("the supervisor exited")
		}
		return line
	case <-time.After(patience):
		s.t.Fatalf("the supervisor said nothing for %s", patience)
	}
	return ""
}

// alive reports whether a process this test started is still that process
// and is not a zombie.
func stillRunning(pid int, ticks uint64) bool {
	if processStartTicks(pid) != ticks {
		return false
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	end := bytes.LastIndexByte(raw, ')')
	state := strings.Fields(string(raw[end+1:]))[0]
	return state != "Z" && state != "X"
}

// requireBuildAlive fails unless the barrier's leader AND its child are both
// still running, a moment after whatever the test just did.
func (s *supervised) requireBuildAlive(during string) {
	s.t.Helper()
	time.Sleep(150 * time.Millisecond) // a SIGKILL is delivered well inside this
	if !stillRunning(s.leader, s.leaderTicks) || !stillRunning(s.child, s.childTicks) {
		s.t.Fatalf("%s stopped the original build (leader %d alive=%v, child %d alive=%v)", during,
			s.leader, stillRunning(s.leader, s.leaderTicks), s.child, stillRunning(s.child, s.childTicks))
	}
}

// requireBuildGone waits for both barrier processes to have gone.
func (s *supervised) requireBuildGone(during string) {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !stillRunning(s.leader, s.leaderTicks) && !stillRunning(s.child, s.childTicks) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.t.Fatalf("%s left the abandoned build running (leader alive=%v, child alive=%v)", during,
		stillRunning(s.leader, s.leaderTicks), stillRunning(s.child, s.childTicks))
}

// release opens the barrier in the ORIGINAL workspace.
func (s *supervised) release() {
	if err := os.WriteFile(filepath.Join(s.workspace(), "release"), []byte("go\n"), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

// crash is a kill -9 of the supervisor, by the handle this test holds.
func (s *supervised) crash() {
	s.t.Helper()
	if err := s.cmd.Process.Kill(); err != nil {
		s.t.Fatal(err)
	}
	_ = s.cmd.Wait()
}

func (s *supervised) cleanup() {
	if s.cmd.ProcessState == nil {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	}
	// Only the processes this test started, and only while they are still
	// them: a pid whose start time changed belongs to somebody else now.
	for _, p := range []struct {
		pid   int
		ticks uint64
	}{{s.leader, s.leaderTicks}, {s.child, s.childTicks}} {
		if p.pid > 0 && stillRunning(p.pid, p.ticks) {
			_ = syscall.Kill(p.pid, syscall.SIGKILL)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (stillRunning(s.leader, s.leaderTicks) || stillRunning(s.child, s.childTicks)) {
		time.Sleep(20 * time.Millisecond)
	}
	if s.leader > 0 && (stillRunning(s.leader, s.leaderTicks) || stillRunning(s.child, s.childTicks)) {
		s.t.Errorf("a barrier process this test started survived its cleanup (leader %d, child %d)", s.leader, s.child)
	}
}

// copyStore copies a job store the way a person backing one up would: every
// file's bytes and mode, into a different directory.
func copyStore(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path != from {
			return nil // a heartbeat's temporary file, renamed away mid-copy
		}
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		target := filepath.Join(to, rel)
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
	if err != nil {
		t.Fatalf("copying the store: %v", err)
	}
}

// copyActiveStore copies the supervisor's store to a new directory and ages
// ONLY the copy's heartbeat, so the copy's recovery takes the job at once.
func copyActiveStore(t *testing.T, s *supervised) (*Store, []byte) {
	t.Helper()
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
	if err := copied.Heartbeat(s.id, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	return copied, record
}

// requireOriginalUntouched: the original's record is byte for byte what it was
// before the copy, and its own supervisor is still keeping it fresh.
func (s *supervised) requireOriginalUntouched(record []byte) {
	s.t.Helper()
	now, err := os.ReadFile(filepath.Join(s.jobs, s.id, recordName))
	if err != nil {
		s.t.Fatal(err)
	}
	if !bytes.Equal(now, record) {
		s.t.Errorf("the original record changed:\n%s", now)
	}
	if age, found := s.store().heartbeatAge(s.id, time.Now()); !found || age >= heartbeatStale {
		s.t.Errorf("the original heartbeat is %v old (found %v)", age, found)
	}
}

// finishOriginal releases the barrier and requires the original job to end
// the ordinary way.
func (s *supervised) finishOriginal() {
	s.t.Helper()
	s.release()
	if line := s.nextLine(60 * time.Second); line != "done succeeded" {
		record, _ := s.store().Load(s.id)
		s.t.Fatalf("the original job ended %q (error %q)", line, record.Error)
	}
}

// R02 — Store.Recover on a copy, while the original build waits at a barrier.
func TestR02RecoveringACopiedStoreLeavesTheOriginalBuildRunning(t *testing.T) {
	s := startSupervisor(t)
	copied, record := copyActiveStore(t, s)

	recovered, err := copied.Recover(time.Now().UTC())
	if err != nil {
		t.Fatalf("recovering the copy: %v", err)
	}
	s.requireBuildAlive("recovering a copied store")
	s.requireOriginalUntouched(record)
	if len(recovered) != 1 || recovered[0].Outcome != RecoveryForeignStore || recovered[0].Signalled {
		t.Errorf("the copy decided %+v, want foreign_store and nothing signalled", recovered)
	}
	requireCopiedHistory(t, copied, s.id)
	s.finishOriginal()
}

// requireCopiedHistory: the copy's own record says what it knows — copied,
// interrupted HERE, nothing stopped — and claims neither that the original
// Companion crashed nor how the original build ended.
func requireCopiedHistory(t *testing.T, copied *Store, id string) {
	t.Helper()
	j, err := copied.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != Interrupted || j.Recovery == nil || j.Recovery.Outcome != RecoveryForeignStore || j.Recovery.Signalled {
		t.Fatalf("the copy's record is %s with recovery %+v", j.State, j.Recovery)
	}
	for _, want := range []string{"copied, moved or restored here", "no program was stopped"} {
		if !strings.Contains(j.Error, want) {
			t.Errorf("the copy's record says %q, want it to say %q", j.Error, want)
		}
	}
	for _, claim := range []string{"were stopped", "the Companion running this job stopped", "crash"} {
		if strings.Contains(j.Error, claim) {
			t.Errorf("the copy's record claims %q: %q", claim, j.Error)
		}
	}
}

// R03 — the same copy, through the production constructor and Start.
func TestR03StartingAServiceOnACopiedStoreLeavesTheOriginalBuildRunning(t *testing.T) {
	s := startSupervisor(t)
	copied, record := copyActiveStore(t, s)

	service, err := NewService(Options{Store: copied, Catalog: NewCatalog(filepath.Join(t.TempDir(), "profiles"))})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatalf("starting a service on the copy: %v", err)
	}
	defer service.Close()
	s.requireBuildAlive("starting a Companion on a copied store")
	s.requireOriginalUntouched(record)
	requireCopiedHistory(t, copied, s.id)
	s.finishOriginal()
}

// testLogWriter sends a child's stderr to the test log.
type testLogWriter struct {
	t      *testing.T
	prefix string
}

func (w *testLogWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		w.t.Log(w.prefix + line)
	}
	return len(p), nil
}
