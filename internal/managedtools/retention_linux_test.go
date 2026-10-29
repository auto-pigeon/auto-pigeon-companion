package managedtools_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	mt "github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools/managedtoolstest"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/tooluse"
)

// 01_NEW_368 §2: retention honours every live reference, not just active and
// previous. A compiler whose Companion was killed keeps running from 1.4536;
// three later updates move active and previous past it, and its installation
// is still not removed — by the job's own verified process identity — until
// it ends. Nothing signals it.
func TestAnOlderInstallationAnOrphanedCompilerRunsFromSurvivesLaterUpdates(t *testing.T) {
	r, _, _ := offered(t)
	uses, err := tooluse.Open(r.root)
	if err != nil {
		t.Fatal(err)
	}
	m := r.manager(func(o *managedtools.Options) { o.Uses = uses })
	oldest := tool(m.Status(), managedtools.EricW).Active.Dir

	// What a Companion killed mid-build leaves: its lease, a job record it
	// launched, and the compiler itself, still running.
	sleeper := exec.Command("sleep", "300")
	sleeper.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() }()
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	parent := job.ProcessIdentity{PID: dead.Process.Pid, StartTicks: 1}
	since := time.Now().Add(-time.Minute)
	stores := filepath.Join(t.TempDir(), "jobs")
	store, err := job.OpenStore(stores)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := job.NewID(time.Now())
	if err := store.Save(&job.Job{SchemaVersion: job.SchemaVersion, ID: id, State: job.Running,
		ProfileID: "auto-pigeon.ericw-tools.q1", ActionID: "vis", CreatedAt: time.Now(),
		Owner:   job.Owner{PID: parent.PID},
		Process: job.ProcessIdentity{PID: sleeper.Process.Pid, StartTicks: job.ProcessStartTicks(sleeper.Process.Pid)},
		Launch:  &job.Launch{Supervisor: parent}}); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	record, _ := json.Marshal(tooluse.Record{Schema: tooluse.Schema, Kind: "use", Tools: []string{"companion", "ericw-tools"},
		Dirs: []string{oldest}, Stores: []string{store.Root()}, Program: "companion build run", Host: host,
		PID: parent.PID, StartTicks: parent.StartTicks, Since: since})
	if err := os.WriteFile(filepath.Join(uses.Root(), tooluse.DirName, "use-killed.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, version := range []string{"1.4540", "1.4541", "1.4542", "1.4543"} {
		if version != "1.4540" {
			r.publish(ericwRepo, mt.Spec{Tool: managedtools.EricW, Version: version, Prerelease: true})
			r.clock.Advance(2 * time.Hour)
			m.Step(context.Background())
		}
		offer := tool(m.Status(), managedtools.EricW).Offer
		if offer == nil {
			t.Fatalf("no offer of %s", version)
		}
		_ = m.Decide(managedtools.EricW, managedtools.DecideNow, offer.Identity(), false)
		m.Step(context.Background())
		m.Step(context.Background())
		if got := tool(m.Status(), managedtools.EricW).Active.Version; got != version {
			t.Fatalf("the orphan held the switch to %s (it must not: it runs from absolute paths): active %s", version, got)
		}
	}
	if _, err := os.Stat(oldest); err != nil {
		t.Fatalf("the installation an orphaned compiler runs from was removed: %v", err)
	}
	if err := syscall.Kill(sleeper.Process.Pid, 0); err != nil {
		t.Fatalf("the orphan was signalled: %v", err)
	}
	_ = sleeper.Process.Kill()
	_ = sleeper.Wait()
	m.Step(context.Background())
	if _, err := os.Stat(oldest); !os.IsNotExist(err) {
		t.Fatalf("after the orphan ended its installation is still kept: %v", err)
	}
}
