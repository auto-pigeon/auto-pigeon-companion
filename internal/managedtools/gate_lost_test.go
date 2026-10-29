package managedtools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/tooluse"
)

// NEW_368 (gate ownership): an update waits for a build holding its switch
// gate. Meanwhile its gate is reclaimed — its process stopped heartbeating,
// as across a machine's sleep — and another switcher publishes its own. When
// the build ends, the update does not activate on the strength of the gate it
// once had, and leaves the other switcher's gate alone; it activates once it
// holds the gate again.
func TestAnUpdateWhoseGateWasReclaimedDoesNotActivateUnderAnotherSwitchersGate(t *testing.T) {
	r, _, _ := offered(t)
	uses, err := tooluse.Open(r.root)
	if err != nil {
		t.Fatal(err)
	}
	m := r.manager(func(o *managedtools.Options) { o.Uses = uses })
	offer := tool(m.Status(), managedtools.EricW).Offer
	if offer == nil {
		t.Fatal("no offer")
	}
	r.host.setBusy("Build — q1-normal")
	if err := m.Decide(managedtools.EricW, managedtools.DecideNow, offer.Identity(), false); err != nil {
		t.Fatal(err)
	}
	m.Step(context.Background())
	if view := tool(m.Status(), managedtools.EricW); view.Pending == nil || !view.Pending.Staged {
		t.Fatalf("the update is not staged and waiting: %+v", view.Pending)
	}

	gatePath := filepath.Join(uses.Root(), tooluse.DirName, "switch-ericw-tools.json")
	if err := os.Remove(gatePath); err != nil {
		t.Fatalf("the waiting update holds no gate: %v", err)
	}
	other, err := tooluse.Open(r.root)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := other.Switch(string(managedtools.EricW), "another Companion")
	if err != nil {
		t.Fatal(err)
	}

	r.host.setBusy()
	m.Step(context.Background())
	view := tool(m.Status(), managedtools.EricW)
	if view.Active.Version != "1.4536" {
		t.Fatalf("the update activated %s under another switcher's gate", view.Active.Version)
	}
	if view.Pending == nil || !strings.Contains(strings.Join(view.Pending.Waiting, "; "), "another Companion") {
		t.Fatalf("the update does not say what it waits for: %+v", view.Pending)
	}
	raw, err := os.ReadFile(gatePath)
	var record tooluse.Record
	if err != nil || json.Unmarshal(raw, &record) != nil || record.Program != "another Companion" {
		t.Fatalf("the other switcher's gate was touched: %s (%v)", raw, err)
	}

	theirs.Release()
	m.Step(context.Background())
	if view := tool(m.Status(), managedtools.EricW); view.Active.Version != "1.4540" {
		t.Fatalf("once the gate was free again: active %s, pending %+v", view.Active.Version, view.Pending)
	}
	if _, err := os.Stat(gatePath); !os.IsNotExist(err) {
		t.Fatalf("the update left its gate: %v", err)
	}
}
