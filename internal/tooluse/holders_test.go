package tooluse

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// 02_NEW_368: what replacing the bundle waits for. A parent's death is not
// the end of its compiler, and what cannot be read is not nothing.

func TestAnOrphanedCompilerHoldsTheBundleUntilItEnds(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process identity is read from /proc")
	}
	root := t.TempDir()
	registry := open(t, root)
	parent := startHelper(t, root, "companion,ericw-tools", "orphan", filepath.Join(t.TempDir(), "jobs"), filepath.Join(root, "x"))
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
	_ = parent.command.Process.Kill()
	_ = parent.command.Wait()

	gate, err := registry.Switch("companion", "the Companion replacing itself")
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	holders, err := gate.Holders()
	if err != nil || len(holders) != 1 || !strings.Contains(holders[0].Label, "still running") {
		t.Fatalf("with the parent dead and its compiler running: %+v %v", holders, err)
	}
	if err := syscall.Kill(sleeper, 0); err != nil {
		t.Fatalf("the orphan was signalled: %v", err)
	}
	_ = syscall.Kill(sleeper, syscall.SIGKILL)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if running, _ := job.ProcessRunning(job.ProcessIdentity{PID: sleeper, StartTicks: job.ProcessStartTicks(sleeper)}); !running {
			break
		}
	}
	if holders, err := gate.Holders(); err != nil || len(holders) != 0 {
		t.Fatalf("after the orphan ended: %+v %v", holders, err)
	}
}

func TestAKilledBuildWithNoJobHoldsNothing(t *testing.T) {
	root := t.TempDir()
	registry := open(t, root)
	parent := startHelper(t, root, "companion")
	_ = parent.command.Process.Kill()
	_ = parent.command.Wait()
	gate, err := registry.Switch("companion", "update")
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	var holders []Use
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if holders, err = gate.Holders(); err == nil && len(holders) == 0 {
			return
		}
	}
	t.Fatalf("a dead build still holds: %+v %v", holders, err)
}

func TestARecordThatCannotBeReadHoldsForAStalePeriod(t *testing.T) {
	root := t.TempDir()
	registry := open(t, root)
	gate, err := registry.Switch("companion", "update")
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	path := filepath.Join(registry.dir, "use-torn.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if holders, err := gate.Holders(); err != nil || len(holders) != 1 {
		t.Fatalf("a torn record: %+v %v", holders, err)
	}
	old := time.Now().Add(-2 * staleAfter)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if holders, err := gate.Holders(); err != nil || len(holders) != 0 {
		t.Fatalf("a record stale for a whole period still holds: %+v %v", holders, err)
	}
}

func TestACoordinationFolderThatCannotBeReadIsAnError(t *testing.T) {
	root := t.TempDir()
	registry := open(t, root)
	gate, err := registry.Switch("companion", "update")
	if err != nil {
		t.Fatal(err)
	}
	moved := registry.dir + ".moved"
	if err := os.Rename(registry.dir, moved); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Rename(moved, registry.dir)
		gate.Release()
	}()
	if holders, err := gate.Holders(); err == nil {
		t.Fatalf("nothing could be read and it says %+v", holders)
	}
}
