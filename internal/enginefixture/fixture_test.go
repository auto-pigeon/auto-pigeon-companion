package enginefixture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == enginefixture.Flag {
		os.Exit(enginefixture.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// The fixture's own record is the instrument every other test reads, so it is
// checked here first: an instrument nobody calibrated measures nothing.
func TestFixtureRecordsArgvVerbatim(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "record.json")

	awkward := []string{
		"-basedir", filepath.Join(dir, "Quake — Ünïcode & spaces"),
		"+map", "e1m1; rm -rf $(pwd)",
		"-game", "мод",
	}
	args := append([]string{"--record", record, "--behaviour", string(enginefixture.BehaviourReady), "--"}, awkward...)
	if code := enginefixture.Main(args); code != 0 {
		t.Fatalf("the fixture exited %d, want 0", code)
	}

	got, err := enginefixture.ReadRecord(record)
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	if len(got.Argv) != len(awkward) {
		t.Fatalf("recorded %d arguments, want %d: %q", len(got.Argv), len(awkward), got.Argv)
	}
	for i := range awkward {
		if got.Argv[i] != awkward[i] {
			t.Errorf("argument %d is %q, want %q", i, got.Argv[i], awkward[i])
		}
	}
	if got.PID != os.Getpid() {
		t.Errorf("recorded pid %d, want %d", got.PID, os.Getpid())
	}
}

func TestFixtureCrashes(t *testing.T) {
	code := enginefixture.Main([]string{"--behaviour", string(enginefixture.BehaviourCrash)})
	if code == 0 {
		t.Fatalf("a crashing engine exited 0")
	}
}

func TestFixtureRefusesToStartWithoutTheDataItWasToldToNeed(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "id1", "pak0.pak")
	if code := enginefixture.Main([]string{"--require-file", missing}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
}

func TestFixtureRejectsAnUnknownFlag(t *testing.T) {
	if code := enginefixture.Main([]string{"--nonsense"}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

// The fixture profile is a profile like any other, so it is validated like any
// other. A fixture document that only this package's decoder accepted would be
// proving something about a format nothing else uses.
func TestFixtureProfileIsValid(t *testing.T) {
	document, err := profile.Decode(enginefixture.ProfileJSON)
	if err != nil {
		t.Fatalf("the fixture profile is not valid: %v", err)
	}
	if document.Metadata().ID != enginefixture.ProfileID {
		t.Fatalf("the fixture profile is %q, want %q", document.Metadata().ID, enginefixture.ProfileID)
	}
	engine, ok := document.(*profile.EngineProfile)
	if !ok {
		t.Fatalf("the fixture profile decoded as %T, want an engine profile", document)
	}
	for _, want := range profile.EngineActions {
		if _, found := engine.ActionByID(want); !found {
			t.Errorf("the fixture profile has no %q action; it is the one document that must exercise all five", want)
		}
	}
	if !strings.Contains(engine.Summary, "records") {
		t.Errorf("the fixture profile's summary should say what it is: %q", engine.Summary)
	}
}
