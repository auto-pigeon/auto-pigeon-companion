package retiredprofile

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin/retiredfixture"
)

var now = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func stored(t *testing.T, path string, bindings ...binding.LocalBinding) {
	t.Helper()
	set := binding.NewSet()
	for _, local := range bindings {
		if local.ProfileDigest == "" {
			local.ProfileDigest = "sha256:" + strings.Repeat("0", 64)
		}
		if err := set.Put(local); err != nil {
			t.Fatal(err)
		}
	}
	if err := binding.SaveFile(path, set); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path, id string) (binding.LocalBinding, bool) {
	t.Helper()
	set, err := binding.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return set.Find(id)
}

func machine(t *testing.T) (bindings string, catalog job.Catalog) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "bindings.json"), job.NewCatalog(filepath.Join(dir, "profiles"))
}

// The compatible case: what a person set moves; their program does not, and the
// binding they had stays, unchanged, as the record of it.
func TestFoldersAndArgumentsMoveToTheForkAndProgramsDoNot(t *testing.T) {
	path, catalog := machine(t)
	upstream := "/opt/ericw-tools-2.0.0-alpha11/qbsp"
	stored(t, path, binding.LocalBinding{ProfileID: retiredfixture.EricwQ2, Trust: profile.TrustBuiltin,
		Acquisition: profile.AcquireUserPath, Executables: map[string]string{"qbsp": upstream},
		Roots:     map[string]string{profile.RootGame: "/games/quake2", profile.RootContent: "/games/quake2/baseq2", profile.RootToolInstall: "/opt/ericw-tools-2.0.0-alpha11"},
		Arguments: map[string][]string{"qbsp": {"-worldextent", "65536"}}, GameProfileID: "game-record", Compatibility: nil})
	done, err := Apply(path, catalog, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 1 || done[0].Replacement != builtin.EricwAutoPigeonQ2 || !done[0].OwnPrograms {
		t.Fatalf("done = %+v", done)
	}
	fork, found := read(t, path, builtin.EricwAutoPigeonQ2)
	if !found {
		t.Fatal("the fork has no binding")
	}
	if len(fork.Executables) != 0 || fork.Managed != nil {
		t.Fatalf("an upstream program was relabelled the fork: %+v", fork)
	}
	if fork.Roots[profile.RootGame] != "/games/quake2" || fork.Roots[profile.RootContent] != "/games/quake2/baseq2" || fork.GameProfileID != "game-record" {
		t.Errorf("a setting was lost: %+v", fork)
	}
	if _, kept := fork.Roots[profile.RootToolInstall]; kept {
		t.Errorf("where the upstream program was unpacked moved to the fork: %v", fork.Roots)
	}
	if !slices.Equal(fork.Arguments["qbsp"], []string{"-worldextent", "65536"}) {
		t.Errorf("arguments: %v", fork.Arguments)
	}
	old, found := read(t, path, retiredfixture.EricwQ2)
	if !found || old.Executables["qbsp"] != upstream {
		t.Errorf("the retired binding was changed: %+v", old)
	}
	// Once. A folder removed from the fork afterwards is not put back.
	if _, err := binding.Update(path, func(set *binding.Set) error {
		fork, _ := set.Find(builtin.EricwAutoPigeonQ2)
		delete(fork.Roots, profile.RootContent)
		return set.Put(fork)
	}); err != nil {
		t.Fatal(err)
	}
	again, err := Apply(path, catalog, now.Add(time.Hour))
	if err != nil || len(again) != 0 {
		t.Fatalf("a second run: %v %v", again, err)
	}
	if fork, _ := read(t, path, builtin.EricwAutoPigeonQ2); fork.Roots[profile.RootContent] != "" {
		t.Error("a setting the person removed came back")
	}
	if entry, found := Load(path).For(builtin.EricwAutoPigeonQ2); !found || !entry.OwnPrograms || entry.At != now {
		t.Errorf("the record: %+v", entry)
	}
}

// An argument the maintained document already manages cannot be added beside it:
// it is left out by name, never stripped silently.
func TestAnArgumentTheForkManagesIsNotCarriedAndIsNamed(t *testing.T) {
	path, catalog := machine(t)
	stored(t, path, binding.LocalBinding{ProfileID: retiredfixture.EricwQ1, Trust: profile.TrustBuiltin,
		Arguments: map[string][]string{"qbsp": {"-leaktest"}, "vis": {"-noambient"}, "gone": {"-x"}}})
	done, err := Apply(path, catalog, now)
	if err != nil || len(done) != 1 {
		t.Fatalf("%+v %v", done, err)
	}
	if done[0].OwnPrograms {
		t.Error("no program was chosen, and the record says one was")
	}
	refused := map[string]string{}
	for _, item := range done[0].NotCarried {
		refused[item.What] = item.Why
	}
	if !strings.Contains(refused["arguments:qbsp"], "-leaktest") || !strings.Contains(refused["arguments:gone"], "no program") {
		t.Errorf("refused: %v", refused)
	}
	fork, _ := read(t, path, builtin.EricwAutoPigeonQ1)
	if _, carried := fork.Arguments["qbsp"]; carried || !slices.Equal(fork.Arguments["vis"], []string{"-noambient"}) {
		t.Errorf("arguments: %v", fork.Arguments)
	}
}

// A binding that is already the fork's keeps what it has.
func TestWhatTheForkAlreadyHasIsKept(t *testing.T) {
	path, catalog := machine(t)
	stored(t, path,
		binding.LocalBinding{ProfileID: retiredfixture.EricwQ2, Trust: profile.TrustBuiltin, Roots: map[string]string{profile.RootGame: "/old"}},
		binding.LocalBinding{ProfileID: builtin.EricwAutoPigeonQ2, Trust: profile.TrustBuiltin, Roots: map[string]string{profile.RootGame: "/mine"},
			Executables: map[string]string{"qbsp": "/mine/bin/qbsp"}})
	done, err := Apply(path, catalog, now)
	if err != nil || len(done) != 1 || len(done[0].NotCarried) != 1 {
		t.Fatalf("%+v %v", done, err)
	}
	fork, _ := read(t, path, builtin.EricwAutoPigeonQ2)
	if fork.Roots[profile.RootGame] != "/mine" || fork.Executables["qbsp"] != "/mine/bin/qbsp" {
		t.Errorf("the fork's own settings were replaced: %+v", fork)
	}
}

// A pipeline's stage arguments follow the pipeline that was folded into another.
func TestStageArgumentsFollowTheRetiredQuake3LeakTest(t *testing.T) {
	path, catalog := machine(t)
	stored(t, path, binding.LocalBinding{ProfileID: "auto-pigeon.q3.leak-test.auto-pigeon-build", Trust: profile.TrustBuiltin,
		StepArguments: map[string][]string{"compile": {"-nofog"}, "vis": {"-fast"}}})
	done, err := Apply(path, catalog, now)
	if err != nil || len(done) != 1 {
		t.Fatalf("%+v %v", done, err)
	}
	pipeline, _ := read(t, path, builtin.Q3LeakTest)
	if !slices.Equal(pipeline.StepArguments["compile"], []string{"-nofog"}) {
		t.Errorf("stage arguments: %v", pipeline.StepArguments)
	}
	if len(done[0].NotCarried) != 1 || done[0].NotCarried[0].What != "stage_arguments:vis" {
		t.Errorf("a stage the replacement does not have: %+v", done[0].NotCarried)
	}
}

// A person's own document that declares a retired id is theirs: nothing looks at
// its binding, nothing is carried and nothing is promoted.
func TestAPersonsOwnDocumentWithARetiredIDIsLeftAlone(t *testing.T) {
	path, _ := machine(t)
	dir := filepath.Dir(path)
	raw, err := retiredfixture.Raw(retiredfixture.EricwQ1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles", "mine.tool.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := job.NewCatalog(filepath.Join(dir, "profiles"))
	stored(t, path, binding.LocalBinding{ProfileID: retiredfixture.EricwQ1, Trust: profile.TrustLocal,
		Roots: map[string]string{profile.RootContent: "/mine"}})
	done, err := Apply(path, catalog, now)
	if err != nil || len(done) != 0 {
		t.Fatalf("%+v %v", done, err)
	}
	if _, found := read(t, path, builtin.EricwAutoPigeonQ1); found {
		t.Error("a person's own record was carried onto the fork")
	}
	entry, err := catalog.Lookup(retiredfixture.EricwQ1)
	if err != nil || entry.Trust != profile.TrustLocal {
		t.Errorf("the person's document: trust %q, %v", entry.Trust, err)
	}
	if builtin.Current(retiredfixture.EricwQ1, func(string) bool { return true }) != retiredfixture.EricwQ1 {
		t.Error("a reference to a person's own document was moved to the fork")
	}
}

func TestAMachineWithNoBindingsHasNothingToDo(t *testing.T) {
	path, catalog := machine(t)
	if done, err := Apply(path, catalog, now); err != nil || len(done) != 0 {
		t.Fatalf("%+v %v", done, err)
	}
	if _, err := os.Stat(Path(path)); !os.IsNotExist(err) {
		t.Errorf("a record was written for nothing: %v", err)
	}
}
