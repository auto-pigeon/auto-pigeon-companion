package q2run

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2fixture"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2install"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2pack"
)

// The lines are Yamagi Quake II 8.70's own, as it printed them on a pipe while
// Q2_009 measured it (`LLM/runs/auto-pigeon/Q2_009/measure`): `Map:` is
// printed with a carriage return and no newline, so the next line is glued to
// it.
const (
	lineConsole = "Console initialized."
	lineNoMap   = "Can't find maps/q2009room.bsp"
	lineNoGame  = "ERROR: failed to load game DLL"
)

func lineOpened(path string) string {
	return "FS_FOpenFile: 'maps/q2009room.bsp' (found in '" + path + "')."
}

func lineMap(next string) string { return "Map: q2009room\r" + next }

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
	f.current = job.Job{ID: "j-q2", State: job.Queued}
	return f.current.Clone(), nil
}

func (f *fakeJobs) Preview(request job.Request) (*job.Job, error) {
	return &job.Job{Command: &job.CommandPreview{Executable: "/opt/yamagi/quake2",
		Args: []string{"-datadir", request.Roots[profile.RootGame], "+set", "game", request.Runtime[profile.RuntimeModName]}}}, nil
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

func (f *fakeJobs) engine(lines []string) {
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
		f.current.Process = job.ProcessIdentity{PID: 4343}
		mirror := f.mirror
		f.mu.Unlock()
		for _, line := range lines {
			_, _ = mirror.Write([]byte(line + "\n"))
		}
	}()
}

type catalog struct{ entries map[string]job.CatalogEntry }

func (c catalog) Lookup(id string) (job.CatalogEntry, error) {
	entry, ok := c.entries[id]
	if !ok {
		return job.CatalogEntry{}, job.ErrNoProfile
	}
	return entry, nil
}
func (c catalog) List() ([]job.CatalogEntry, error) { return nil, nil }

func installed(t *testing.T) (*q2install.Installation, *fakeJobs) {
	t.Helper()
	root := t.TempDir()
	game := filepath.Join(root, "Quake II")
	if _, err := q2fixture.Write(filepath.Join(game, "baseq2"), map[string][]byte{
		"pak0.pak": q2fixture.PAK([]string{"pics/colormap.pcx"}, map[string][]byte{"pics/colormap.pcx": q2fixture.PCX()})}); err != nil {
		t.Fatal(err)
	}
	bsp := filepath.Join(root, "q2009room.bsp")
	os.WriteFile(bsp, q2fixture.BSP(`{ "classname" "worldspawn" }`), 0o644)
	plan, err := q2pack.Prepare(q2pack.Request{Manifest: &build.Manifest{BuildID: "b", EngineFamily: "quake2", State: job.Succeeded,
		Inputs:  []build.FileRecord{{Name: "source_map", Path: "q2009room.map"}},
		Roots:   []build.RootRecord{{Role: profile.RootGame, Path: filepath.Join(game, "baseq2")}},
		Outputs: []build.FileRecord{{Name: "bsp", Path: bsp}}}})
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := q2pack.Store{Dir: filepath.Join(root, "packages")}.Create(plan, q2pack.Options{})
	if err != nil {
		t.Fatal(err)
	}
	installation, err := q2install.Install(context.Background(), q2install.Request{Package: record, GameRoot: game, Dir: filepath.Join(root, "installs")})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := builtin.Find(builtin.YamagiQuake2)
	if err != nil {
		t.Fatal(err)
	}
	return installation, &fakeJobs{catalog: catalog{entries: map[string]job.CatalogEntry{
		builtin.YamagiQuake2: {Profile: entry.Profile, Digest: entry.Digest}}}}
}

func run(t *testing.T, installation *q2install.Installation, jobs *fakeJobs, wait time.Duration) *Result {
	t.Helper()
	result, err := Launch(context.Background(), jobs, Request{Installation: installation,
		EngineProfileID: builtin.YamagiQuake2, ActionID: profile.ActionPlayMap, Wait: wait})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestARunIsAcceptedOnlyWhenTheEngineOpenedTheMapFromTheInstalledPackage(t *testing.T) {
	installation, jobs := installed(t)
	jobs.engine([]string{lineConsole, lineOpened(installation.Archive.Path),
		lineMap(lineOpened(installation.Archive.Path))})
	result := run(t, installation, jobs, 5*time.Second)
	if result.MapLoad != Accepted || !result.SourceVerified || result.LoadedFrom != installation.Archive.Path {
		t.Fatalf("result %+v", result)
	}
	if jobs.cancelled {
		t.Error("a loaded engine was stopped")
	}
	if jobs.submitted.Runtime[profile.RuntimeModName] != "aup-q2009room" || jobs.submitted.Runtime[profile.RuntimeMapName] != "q2009room" ||
		jobs.submitted.Roots[profile.RootGame] != installation.GameRoot {
		t.Errorf("the engine was asked for %+v", jobs.submitted)
	}
}

func TestAMapFoundSomewhereTheEngineSearchesFirstIsRefused(t *testing.T) {
	installation, jobs := installed(t)
	// Measured: a loose map in Yamagi's own write folder for the mod wins.
	shadow := "/home/player/.local/share/YamagiQ2/aup-q2009room"
	jobs.engine([]string{lineOpened(shadow), lineMap(lineOpened(shadow))})
	result := run(t, installation, jobs, 5*time.Second)
	if result.MapLoad != Refused || result.FailureClass != failure.LoadOrderShadowed || result.LoadedFrom != shadow {
		t.Fatalf("result %+v", result)
	}
	if !jobs.cancelled || !result.EngineStopped {
		t.Error("an engine playing the wrong map was left running")
	}
}

func TestTheEnginesOwnRefusalsAreNamed(t *testing.T) {
	for line, rule := range map[string]string{lineNoMap: "map_missing", lineNoGame: "gamecode_missing",
		// Measured through the Companion before -cfgdir (Q2_009): every launch.
		"Error: recursive error after: Couldn't create dir /jobs/x/home/.local/share/YamagiQ2/": "settings_folder",
		"Error: No usable renderer found!": "renderer_missing"} {
		installation, jobs := installed(t)
		jobs.engine([]string{lineConsole, line})
		result := run(t, installation, jobs, 5*time.Second)
		if result.MapLoad != Refused || result.RuleID != rule {
			t.Errorf("%q: %+v", line, result)
		}
	}
}

func TestALoadWithoutTheEnginesWordForTheFileIsNotAccepted(t *testing.T) {
	installation, jobs := installed(t)
	jobs.engine([]string{lineConsole, "Map: q2009room"})
	result := run(t, installation, jobs, 5*time.Second)
	if result.MapLoad != NotObserved || result.SourceVerified {
		t.Fatalf("result %+v", result)
	}
	// And the wait is bounded: silence is not a success either.
	installation, jobs = installed(t)
	jobs.engine([]string{lineConsole})
	result = run(t, installation, jobs, 300*time.Millisecond)
	if result.MapLoad != NotObserved || !strings.Contains(result.Message, "did not report loading the map") {
		t.Fatalf("silence: %+v", result)
	}
}

func TestAnInstallationChangedSinceItWasInstalledIsNotStarted(t *testing.T) {
	installation, jobs := installed(t)
	os.WriteFile(installation.Archive.Path, []byte("PACK"), 0o644)
	_, err := Launch(context.Background(), jobs, Request{Installation: installation,
		EngineProfileID: builtin.YamagiQuake2, ActionID: profile.ActionPlayMap})
	if failure.Of(err) != failure.ArchiveDamaged || jobs.mirror != nil {
		t.Fatalf("a changed installation: %v (started: %v)", err, jobs.mirror != nil)
	}
}
