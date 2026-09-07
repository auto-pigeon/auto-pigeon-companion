package engine_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/enginefixture"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// Launching, end to end, against a program that writes down what it was asked
// to do.
//
// The engine here is [enginefixture], re-executed out of this test binary. That
// is what makes these tests say something: a fake that returned canned bytes
// would be a fake whose argv nobody checked, and a real engine would need a
// copy of Quake that is not ours to have. What is proved is the part the
// Companion is responsible for — that the command line the profile describes is
// the command line the process receives, one element at a time.

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == enginefixture.Flag {
		os.Exit(enginefixture.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

// harness is one machine: a profile directory, a binding file, a job store and
// a running executor.
type harness struct {
	t        *testing.T
	service  *job.Service
	game     string
	content  string
	profiles string
}

// newHarness sets up the documents given, each bound to the fixture engine and
// approved. gameName is the game root's directory name, which several tests
// choose deliberately.
func newHarness(t *testing.T, gameName string, documents ...[]byte) *harness {
	t.Helper()
	base := t.TempDir()
	game := filepath.Join(base, gameName)
	writeFile(t, filepath.Join(game, "id1", "pak0.pak"), "PACK")
	content := filepath.Join(base, "project")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	profiles := filepath.Join(base, "profiles")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}

	bindings := binding.NewSet()
	for i, raw := range documents {
		document, err := profile.Decode(raw)
		if err != nil {
			t.Fatalf("document %d: %v", i, err)
		}
		digest, err := profile.Digest(document)
		if err != nil {
			t.Fatalf("document %d: %v", i, err)
		}
		name := filepath.Join(profiles, document.Metadata().ID+".json")
		if err := os.WriteFile(name, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		// `local` trust, because that is what a document found in a directory
		// is: the catalog decides this, not the test, and the grant is what
		// makes it runnable.
		if err := bindings.Put(binding.LocalBinding{
			ProfileID:      document.Metadata().ID,
			ProfileVersion: document.Metadata().Version,
			ProfileDigest:  digest,
			Trust:          profile.TrustLocal,
			Acquisition:    profile.AcquireUserPath,
			Executables:    map[string]string{"engine": self},
			Roots:          map[string]string{profile.RootGame: game, profile.RootContent: content},
			Grant:          profile.NewGrant(document, profile.TrustLocal, digest, time.Now()),
		}); err != nil {
			t.Fatalf("document %d: %v", i, err)
		}
	}
	bindingsPath := filepath.Join(base, "bindings.json")
	if err := binding.SaveFile(bindingsPath, bindings); err != nil {
		t.Fatal(err)
	}

	store, err := job.OpenStore(filepath.Join(base, "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := job.NewService(job.Options{
		Store:    store,
		Catalog:  job.NewCatalog(profiles),
		Bindings: binding.Lookup(bindingsPath),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		service.Close()
	})
	return &harness{t: t, service: service, game: game, content: content, profiles: profiles}
}

func fixtureHarness(t *testing.T) *harness {
	t.Helper()
	return newHarness(t, "quake", enginefixture.ProfileJSON)
}

// run submits one action and waits for it.
func (h *harness) run(action string, runtimeValues, options map[string]string) *job.Job {
	h.t.Helper()
	submitted, err := h.service.Submit(job.Request{
		ProfileID: enginefixture.ProfileID,
		ActionID:  action,
		Runtime:   runtimeValues,
		Options:   options,
	})
	if err != nil {
		h.t.Fatalf("submitting %s: %v", action, err)
	}
	finished, err := h.service.Wait(context.Background(), submitted.ID)
	if err != nil {
		h.t.Fatalf("waiting for %s: %v", action, err)
	}
	return finished
}

// record is what the engine wrote down about how it was started.
func (h *harness) record() enginefixture.Record {
	h.t.Helper()
	got, err := enginefixture.ReadRecord(filepath.Join(h.content, enginefixture.RecordName))
	if err != nil {
		h.t.Fatalf("%v", err)
	}
	return got
}

func joined(argv []string) string { return strings.Join(argv, " ") }

// Every action the profile declares produces the argv the profile describes,
// and arrives at the process with its session role intact. This is the whole
// claim of the engine model in one test.
func TestEveryEngineActionReachesTheProcessAsDescribed(t *testing.T) {
	cases := []struct {
		action  string
		runtime map[string]string
		role    profile.SessionRole
		want    []string
	}{
		{
			action:  profile.ActionPlayMap,
			runtime: map[string]string{"map_name": "e1m1", "mod_name": "mymod"},
			role:    profile.SessionClient,
			want:    []string{"-basedir", "{game}", "-game", "mymod", "+map", "e1m1"},
		},
		{
			action:  profile.ActionPlayPackage,
			runtime: map[string]string{"package_name": "ad_sepulcher"},
			role:    profile.SessionClient,
			want:    []string{"-basedir", "{game}", "-game", "ad_sepulcher"},
		},
		{
			action:  profile.ActionJoinServer,
			runtime: map[string]string{"server_host": "quake.example.org", "server_port": "26000"},
			role:    profile.SessionClient,
			want:    []string{"-basedir", "{game}", "+connect", "quake.example.org:26000"},
		},
		{
			action:  profile.ActionHostListen,
			runtime: map[string]string{"map_name": "dm3", "mod_name": "mymod"},
			role:    profile.SessionListen,
			want:    []string{"-basedir", "{game}", "-game", "mymod", "+maxplayers", "8", "+map", "dm3"},
		},
		{
			action:  profile.ActionHostDedicated,
			runtime: map[string]string{"map_name": "dm3", "mod_name": "mymod"},
			role:    profile.SessionDedicated,
			want:    []string{"-dedicated", "8", "-basedir", "{game}", "-game", "mymod", "+map", "dm3"},
		},
	}
	for _, test := range cases {
		t.Run(test.action, func(t *testing.T) {
			h := fixtureHarness(t)
			finished := h.run(test.action, test.runtime, nil)
			if !finished.Succeeded() {
				t.Fatalf("%s: %s", finished.State, finished.Error)
			}
			if finished.SessionRole != test.role {
				t.Errorf("the job records the session role %q, want %q", finished.SessionRole, test.role)
			}
			want := make([]string, len(test.want))
			for i, part := range test.want {
				want[i] = strings.ReplaceAll(part, "{game}", h.game)
			}
			got := h.record()
			if joined(got.Argv) != joined(want) {
				t.Errorf("argv:\n  got  %q\n  want %q", got.Argv, want)
			}
			// Working-directory control: the profile says the game root, and
			// that is where the process starts.
			resolved := h.game
			if evaluated, err := filepath.EvalSymlinks(h.game); err == nil {
				resolved = evaluated
			}
			if got.WorkingDir != resolved {
				t.Errorf("the engine started in %q, want the game root %q", got.WorkingDir, resolved)
			}
		})
	}
}

// awkwardDir is a directory name that would be several arguments, a redirect
// and a command substitution if anything anywhere split a string.
func awkwardDir() string {
	if runtime.GOOS == "windows" {
		// Windows will not have most of those characters in a path at all, so
		// the part it can test is spaces and non-ASCII.
		return "Quake — Ünïcode and spaces"
	}
	return "Quake — Ünïcode, spaces; $(id) && `whoami`"
}

// Nothing between the document and execve splits, quotes or expands anything,
// because there is nothing between the document and execve. This is the test
// that says so out loud.
func TestAwkwardPathsAndValuesArriveLiterally(t *testing.T) {
	h := newHarness(t, awkwardDir(), enginefixture.ProfileJSON)
	mapName := "e1m1; rm -rf ~ && echo $(whoami)"
	if runtime.GOOS == "windows" {
		mapName = "e1m1 with spaces"
	}
	finished := h.run(profile.ActionPlayMap, map[string]string{"map_name": mapName, "mod_name": "mód dir"}, nil)
	if !finished.Succeeded() {
		t.Fatalf("%s: %s", finished.State, finished.Error)
	}
	got := h.record()
	want := []string{"-basedir", h.game, "-game", "mód dir", "+map", mapName}
	if joined(got.Argv) != joined(want) {
		t.Fatalf("argv:\n  got  %q\n  want %q", got.Argv, want)
	}
	for _, arg := range got.Argv {
		if strings.Contains(arg, "uid=") || strings.Contains(arg, "root:") {
			t.Fatalf("something evaluated an argument: %q", arg)
		}
	}
}

// The environment a game gets is the executor's, not the user's. An engine
// profile that inherits nothing gets nothing, and the check that matters is the
// absence.
func TestTheEngineDoesNotInheritTheCompanionsEnvironment(t *testing.T) {
	t.Setenv("AUCOM_ENGINE_TEST_SECRET", "not-for-the-engine")
	h := fixtureHarness(t)
	finished := h.run(profile.ActionPlayMap, map[string]string{"map_name": "e1m1", "mod_name": "mymod"}, nil)
	if !finished.Succeeded() {
		t.Fatalf("%s: %s", finished.State, finished.Error)
	}
	if value, present := h.record().Lookup("AUCOM_ENGINE_TEST_SECRET"); present {
		t.Fatalf("the engine inherited AUCOM_ENGINE_TEST_SECRET=%q", value)
	}
}

// An engine that fails is a recorded failure with the engine's own words in it,
// not a silence.
func TestACrashingEngineIsRecordedWithItsOwnDiagnosis(t *testing.T) {
	h := fixtureHarness(t)
	finished := h.run(profile.ActionPlayMap,
		map[string]string{"map_name": "e1m1", "mod_name": "mymod"},
		map[string]string{"behaviour": string(enginefixture.BehaviourCrash)})
	if finished.Succeeded() {
		t.Fatal("a crashing engine produced a successful job")
	}
	if finished.ExitCode == nil || *finished.ExitCode == 0 {
		t.Errorf("exit code = %v", finished.ExitCode)
	}
	var fatal bool
	for _, diagnostic := range finished.Diagnostics {
		if diagnostic.Severity == profile.SeverityError {
			fatal = true
		}
	}
	if !fatal {
		t.Errorf("the engine's Host_Error was not classified: %+v", finished.Diagnostics)
	}
	logs, err := h.service.Logs(finished.ID, "stderr", false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(string(logs), "Host_Error") {
		t.Errorf("the engine's own last line is not in the log: %s", logs)
	}
}

// The engine refuses to start without the data it needs, and the Companion
// keeps what it said.
func TestAnEngineThatCannotFindItsDataFailsWithTheEnginesMessage(t *testing.T) {
	h := fixtureHarness(t)
	if err := os.RemoveAll(filepath.Join(h.game, "id1")); err != nil {
		t.Fatal(err)
	}
	finished := h.run(profile.ActionPlayMap,
		map[string]string{"map_name": "e1m1", "mod_name": "mymod"},
		map[string]string{"require_game_data": "true"})
	if finished.Succeeded() {
		t.Fatal("the engine started with no game data")
	}
	logs, err := h.service.Logs(finished.ID, "stderr", false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(string(logs), "pak0.pak") {
		t.Errorf("the engine's message about the missing archive was not kept: %s", logs)
	}
}

// The claim the extension model makes: a document nobody shipped runs by the
// same route as one that did. Nothing in the Go code is asked which of the two
// it is.
func TestAUserAuthoredEngineProfileLaunchesByTheSameRoute(t *testing.T) {
	community, err := os.ReadFile(filepath.Join("testdata", "user-q1-engine.engine.json"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	h := newHarness(t, "quake", enginefixture.ProfileJSON, community)

	document, err := profile.Decode(community)
	if err != nil {
		t.Fatalf("%v", err)
	}
	id := document.Metadata().ID

	// It is in the same catalog the built-in profiles are in, at `local` trust.
	entry, err := job.NewCatalog(h.profiles).Lookup(id)
	if err != nil {
		t.Fatalf("the imported profile is not in the catalog: %v", err)
	}
	if entry.Trust != profile.TrustLocal {
		t.Errorf("an imported document has trust %q; a file that appeared in a directory is not vouched for", entry.Trust)
	}

	for _, test := range []struct {
		action  string
		role    profile.SessionRole
		mapName string
		want    []string
	}{
		{profile.ActionPlayMap, profile.SessionClient, "e1m1",
			[]string{"-window", "-basedir", "{game}", "-game", "mymod", "+map", "e1m1"}},
		{profile.ActionHostDedicated, profile.SessionDedicated, "dm3",
			[]string{"-dedicated", "4", "-basedir", "{game}", "-game", "mymod", "+map", "dm3"}},
	} {
		submitted, err := h.service.Submit(job.Request{
			ProfileID: id,
			ActionID:  test.action,
			Runtime:   map[string]string{"map_name": test.mapName, "mod_name": "mymod"},
		})
		if err != nil {
			t.Fatalf("%s: %v", test.action, err)
		}
		finished, err := h.service.Wait(context.Background(), submitted.ID)
		if err != nil {
			t.Fatalf("%s: %v", test.action, err)
		}
		if !finished.Succeeded() {
			t.Fatalf("%s: %s — %s", test.action, finished.State, finished.Error)
		}
		if finished.SessionRole != test.role {
			t.Errorf("%s: session role %q, want %q", test.action, finished.SessionRole, test.role)
		}
		want := make([]string, len(test.want))
		for i, part := range test.want {
			want[i] = strings.ReplaceAll(part, "{game}", h.game)
		}
		if got := h.record(); joined(got.Argv) != joined(want) {
			t.Errorf("%s argv:\n  got  %q\n  want %q", test.action, got.Argv, want)
		}
	}
}

// Content built somewhere else, staged into a game directory, played, and
// removed — the whole loop, with the engine's own argv as the evidence that it
// was pointed at the staged copy.
func TestContentIsStagedPlayedAndRemoved(t *testing.T) {
	h := fixtureHarness(t)
	source := project(t)

	staging := stagingFor(h, "mymap", source)
	staged, err := staging.Stage()
	if err != nil {
		t.Fatalf("staging: %v", err)
	}
	if len(staged.Stamp.Files) == 0 {
		t.Fatal("nothing was staged")
	}

	finished := h.run(profile.ActionPlayMap, map[string]string{"map_name": "level", "mod_name": "mymap"}, nil)
	if !finished.Succeeded() {
		t.Fatalf("%s: %s", finished.State, finished.Error)
	}
	if got := joined(h.record().Argv); !strings.Contains(got, "-game mymap") {
		t.Errorf("the engine was not pointed at the staged directory: %s", got)
	}
	if !exists(filepath.Join(h.game, "mymap", "maps", "level.bsp")) {
		t.Fatal("the staged map is not where the engine would look for it")
	}

	kept, err := unstageFor(h, "mymap")
	if err != nil {
		t.Fatalf("unstaging: %v", err)
	}
	if len(kept) != 0 {
		t.Errorf("unstaging kept %v", kept)
	}
	if exists(filepath.Join(h.game, "mymap")) {
		t.Error("the staged directory survived cleanup")
	}
	if !exists(filepath.Join(h.game, "id1", "pak0.pak")) {
		t.Error("cleanup reached the base game")
	}
}

// mirror is a writer a test can read while a job is still running.
type mirror struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (m *mirror) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.Write(p)
}

func (m *mirror) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.String()
}
