package q3run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3install"
)

const ioquake3 = "auto-pigeon.engine.ioquake3"

// The lines below are what ioquake3 1.36 printed on this project's own
// measurements (`Q3_011`, `LLM/runs/auto-pigeon/Q3_011/measure`), not lines
// written to make a test pass.
const (
	lineInitGame = `InitGame: \version\ioq3 1.36_GIT_8d2c2b4-2025-05-15 linux-x86_64 May 15 2025\com_gamename\Quake3Arena\com_protocol\71\dmflags\0\fraglimit\20\timelimit\0\g_gametype\0\mapname\room\sv_privateClients\0\sv_hostname\noname`
	lineNoMap    = `Can't find map maps/room.bsp`
	lineSarge    = `ERROR: DEFAULT_MODEL (sarge) failed to register`
	lineNoData   = `Quake 3 data files are missing.`
	lineBot      = `^1Error: BotStartFrame: bot library used before being setup`
)

// fakeJobs is a job service that runs no process: a test writes the engine's
// output itself and decides when the job ends.
type fakeJobs struct {
	catalog job.Catalog

	mu        sync.Mutex
	submitted job.Request
	mirror    io.Writer
	current   job.Job
	cancelled bool
}

func (f *fakeJobs) Catalog() job.Catalog { return f.catalog }

func (f *fakeJobs) SubmitWatched(request job.Request, mirror io.Writer) (*job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitted, f.mirror = request, mirror
	f.current = job.Job{ID: "j-test", State: job.Queued}
	return f.current.Clone(), nil
}

func (f *fakeJobs) Preview(request job.Request) (*job.Job, error) {
	return &job.Job{Command: &job.CommandPreview{
		Executable: "/opt/ioquake3/ioq3ded.x86_64",
		Args:       []string{"+set", "fs_basepath", request.Roots[profile.RootGame], "+map", request.Runtime[profile.RuntimeMapName]},
		WorkingDir: request.Roots[profile.RootGame],
	}}, nil
}

func (f *fakeJobs) Get(string) (*job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current.Clone(), nil
}

func (f *fakeJobs) Cancel(string) (*job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = true
	f.current.State = job.Cancelled
	return f.current.Clone(), nil
}

// engine plays an engine: it starts, prints, and optionally exits.
func (f *fakeJobs) engine(lines []string, exit *int) {
	go func() {
		for {
			f.mu.Lock()
			ready := f.mirror != nil
			f.mu.Unlock()
			if ready {
				break
			}
			time.Sleep(time.Millisecond)
		}
		f.mu.Lock()
		f.current.State = job.Running
		f.current.Process = job.ProcessIdentity{PID: 4242}
		mirror := f.mirror
		f.mu.Unlock()
		for _, line := range lines {
			// In pieces, the way a pipe delivers it.
			half := len(line) / 2
			_, _ = mirror.Write([]byte(line[:half]))
			_, _ = mirror.Write([]byte(line[half:] + "\n"))
		}
		if exit != nil {
			f.mu.Lock()
			f.current.State = job.Succeeded
			if *exit != 0 {
				f.current.State = job.Failed
				f.current.Error = "exit status 3"
			}
			f.current.ExitCode = exit
			f.mu.Unlock()
		}
	}()
}

type staticCatalog struct{ entries map[string]job.CatalogEntry }

func (c staticCatalog) Lookup(id string) (job.CatalogEntry, error) {
	entry, found := c.entries[id]
	if !found {
		return job.CatalogEntry{}, job.ErrNoProfile
	}
	return entry, nil
}

func (c staticCatalog) List() ([]job.CatalogEntry, error) { return nil, nil }

func builtinCatalog(t *testing.T) job.Catalog {
	t.Helper()
	entries, err := builtin.Load()
	if err != nil {
		t.Fatalf("%v", err)
	}
	catalog := staticCatalog{entries: map[string]job.CatalogEntry{}}
	for _, entry := range entries {
		catalog.entries[entry.Profile.Metadata().ID] = job.CatalogEntry{Profile: entry.Profile, Digest: entry.Digest}
	}
	return catalog
}

func installed(t *testing.T) *q3install.Installation {
	t.Helper()
	dir := t.TempDir()
	archive := filepath.Join(dir, "baseq3", "auto-pigeon-room-1.pk3")
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		t.Fatalf("%v", err)
	}
	content := []byte("the archive")
	if err := os.WriteFile(archive, content, 0o644); err != nil {
		t.Fatalf("%v", err)
	}
	sum := sha256.Sum256(content)
	return &q3install.Installation{
		ID: "i-test", PackageID: "p-test", MapName: "room", Kind: q3install.Managed,
		BasePath: dir, BaseGame: "baseq3", Game: "baseq3", FSGame: "baseq3", Complete: true,
		Archive: q3install.Archive{Name: filepath.Base(archive), Path: archive, Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:])},
	}
}

func launch(t *testing.T, action string, lines []string, exit *int, wait time.Duration) (*Result, *fakeJobs) {
	t.Helper()
	jobs := &fakeJobs{catalog: builtinCatalog(t)}
	jobs.engine(lines, exit)
	result, err := Launch(context.Background(), jobs, Request{
		Installation: installed(t), EngineProfileID: ioquake3, ActionID: action, Wait: wait,
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	return result, jobs
}

// The server's own line is what makes a run a success, and the result carries
// the pid and the command, not just "started".
func TestARunIsAcceptedOnTheEnginesOwnLine(t *testing.T) {
	result, jobs := launch(t, "host_dedicated", []string{"Opening IP socket: 0.0.0.0:27960", "Server: room", lineInitGame, lineBot}, nil, 5*time.Second)
	if !result.OK() || result.PID != 4242 || result.RuleID != "map_loaded" || !strings.Contains(result.Evidence, `\mapname\room\`) {
		t.Fatalf("%+v", result)
	}
	if !result.SignalDeclared || result.FSGame != "baseq3" || result.Executable == "" || len(result.Args) == 0 {
		t.Errorf("%+v", result)
	}
	if jobs.cancelled {
		t.Error("an engine that loaded the map was stopped")
	}
	// The engine is given the installation's base path, not the binding's.
	if jobs.submitted.Roots[profile.RootGame] != result.BasePath || jobs.submitted.Runtime[profile.RuntimeModName] != "baseq3" {
		t.Errorf("the engine job: %+v", jobs.submitted)
	}
}

// Measured: an engine told to load a map it cannot find stays up. A run that
// called that a running game would be wrong, and one that left it idling would
// leave a process behind.
func TestAMapTheEngineCannotFindIsARefusalAndTheEngineIsStopped(t *testing.T) {
	result, jobs := launch(t, "host_dedicated", []string{"Opening IP socket: 0.0.0.0:27960", lineNoMap}, nil, 5*time.Second)
	if result.MapLoad != Refused || result.FailureClass != failure.MapNotLoaded || result.Evidence != lineNoMap {
		t.Fatalf("%+v", result)
	}
	if !jobs.cancelled || !result.EngineStopped || result.PID != 4242 {
		t.Errorf("the idle engine was left running: %+v", result)
	}
}

// Missing game data is said as missing game data, with the engine's line.
func TestMissingGameDataIsClassedAndShown(t *testing.T) {
	three := 3
	result, _ := launch(t, "play_map", []string{"ioq3 1.36", lineNoData}, &three, 5*time.Second)
	if result.MapLoad != Refused || result.FailureClass != failure.GameDataMissing || !strings.Contains(result.Hint, "pak0.pk3") {
		t.Fatalf("%+v", result)
	}
	// The client with no player data: the world loads and the client does not.
	result, jobs := launch(t, "play_map", []string{"stitched 0 LoD cracks", lineSarge}, nil, 5*time.Second)
	if result.MapLoad != Refused || result.FailureClass != failure.GameDataMissing || result.RuleID != "player_data_missing" {
		t.Fatalf("%+v", result)
	}
	if !jobs.cancelled {
		t.Error("a client stuck on a missing player model was left running")
	}
}

// An engine that exits without a word is a refusal with its exit status.
func TestAnEngineThatStopsWithoutLoadingIsARefusal(t *testing.T) {
	one := 1
	result, _ := launch(t, "host_dedicated", []string{"tty console mode disabled"}, &one, 5*time.Second)
	if result.MapLoad != Refused || result.FailureClass != failure.EngineStopped || result.ExitCode == nil || *result.ExitCode != 1 {
		t.Fatalf("%+v", result)
	}
	zero := 0
	result, _ = launch(t, "host_dedicated", nil, &zero, 5*time.Second)
	if result.MapLoad != Refused || !strings.Contains(result.Message, "status 0") {
		t.Errorf("an engine that exited 0 without loading the map: %+v", result)
	}
}

// Silence is not success: an engine that says nothing is `not_observed`, and it
// is left running for somebody to look at.
func TestAnEngineThatSaysNothingIsNotObserved(t *testing.T) {
	result, jobs := launch(t, "host_dedicated", []string{"Opening IP socket: 0.0.0.0:27960", lineBot}, nil, 300*time.Millisecond)
	if result.MapLoad != NotObserved || result.OK() || jobs.cancelled || !strings.Contains(result.Message, "did not report") {
		t.Fatalf("%+v", result)
	}
}

// A server that fell back to another map has not loaded this one.
func TestAnotherMapsNameIsNotThisMapLoading(t *testing.T) {
	other := strings.Replace(lineInitGame, `\mapname\room\`, `\mapname\q3dm1\`, 1)
	result, _ := launch(t, "host_dedicated", []string{other}, nil, 5*time.Second)
	if result.MapLoad != Refused || !strings.Contains(result.Message, `"q3dm1"`) {
		t.Fatalf("%+v", result)
	}
}

// A package that lacks something says so even when the map loads.
func TestAnIncompletePackageSaysSoWhenTheMapLoads(t *testing.T) {
	jobs := &fakeJobs{catalog: builtinCatalog(t)}
	jobs.engine([]string{lineInitGame}, nil)
	installation := installed(t)
	installation.Complete, installation.NotCarried = false, []string{"textures/x/a.tga"}
	result, err := Launch(context.Background(), jobs, Request{
		Installation: installation, EngineProfileID: ioquake3, ActionID: "host_dedicated", Wait: 5 * time.Second,
	})
	if err != nil || !result.OK() || !strings.Contains(result.Hint, "textures/x/a.tga") {
		t.Fatalf("%v %+v", err, result)
	}
}

// What is refused before any engine is started.
func TestWhatARunRefusesBeforeStartingAnything(t *testing.T) {
	jobs := &fakeJobs{catalog: builtinCatalog(t)}
	installation := installed(t)
	if _, err := Launch(context.Background(), jobs, Request{Installation: installation, EngineProfileID: "no.such.engine", ActionID: "play_map"}); failure.Of(err) != failure.ToolUnavailable {
		t.Errorf("an engine that is not installed: %v", err)
	}
	if _, err := Launch(context.Background(), jobs, Request{Installation: installation, EngineProfileID: ioquake3, ActionID: "join_server"}); err == nil || !strings.Contains(err.Error(), "loads no map") {
		t.Errorf("an action that loads no map: %v", err)
	}
	// The archive was replaced since it was installed.
	if err := os.WriteFile(installation.Archive.Path, []byte("something else"), 0o644); err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := Launch(context.Background(), jobs, Request{Installation: installation, EngineProfileID: ioquake3, ActionID: "host_dedicated"}); err == nil || !strings.Contains(err.Error(), "not the archive that was installed") {
		t.Errorf("a replaced archive: %v", err)
	}
	if jobs.mirror != nil {
		t.Error("an engine job was submitted for a run that was refused")
	}
}

// A cancelled run stops the engine it started.
func TestACancelledRunStopsItsEngine(t *testing.T) {
	jobs := &fakeJobs{catalog: builtinCatalog(t)}
	jobs.engine([]string{"Opening IP socket: 0.0.0.0:27960"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	result, err := Launch(ctx, jobs, Request{Installation: installed(t), EngineProfileID: ioquake3, ActionID: "host_dedicated", Wait: 30 * time.Second})
	if err != nil || result.FailureClass != failure.Cancelled || !jobs.cancelled || !result.EngineStopped {
		t.Fatalf("%v %+v", err, result)
	}
}

// The profile is where "loaded" is defined, and each action that loads a map
// names its line.
func TestEveryQuake3ActionThatLoadsAMapNamesTheLineThatSaysSo(t *testing.T) {
	catalog := builtinCatalog(t)
	for _, id := range []string{ioquake3, "auto-pigeon.engine.q3-generic"} {
		entry, err := catalog.Lookup(id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		for _, action := range entry.Profile.ActionList() {
			if !runsAMap(action) {
				continue
			}
			if !newWatcher(action.Diagnostics, "x").declared {
				t.Errorf("%s %s loads a map and names no map_loaded line", id, action.ID)
			}
		}
	}
}
