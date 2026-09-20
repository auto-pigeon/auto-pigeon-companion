package playrun_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/build"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/playrun"
)

// The coordinator is tested without a network, a compiler or a game installed,
// which is the whole reason its stages arrive as functions. What is under test
// is the ORDER, the RECORD and the CANCELLATION — the three things this package
// actually owns.

type harness struct {
	t       *testing.T
	service *playrun.Service
	store   *playrun.Store

	mu     sync.Mutex
	called []string

	// Each stage's behaviour, overridable per test.
	fetchMap    func(context.Context, playrun.Request) (playrun.MapResult, error)
	fetchBundle func(context.Context, playrun.Request) (playrun.BundleResult, error)
	convert     func(context.Context, playrun.Request, string) (playrun.ConvertResult, error)
	buildRun    func(context.Context, build.Request, func(*build.Manifest)) (*build.Manifest, error)
	install     func(context.Context, playrun.Request, playrun.InstallPlan) (playrun.InstallResult, error)
	verify      func(context.Context, playrun.Request, []playrun.StagedFile) error
	launch      func(context.Context, playrun.Request) (playrun.LaunchRecord, error)
	unstaged    int
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	store, err := playrun.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, store: store}
	h.fetchMap = func(context.Context, playrun.Request) (playrun.MapResult, error) {
		return playrun.MapResult{
			Path: "/cache/dm1.apmap",
			Source: &build.SourceRef{
				Backend: "https://aub.example.test", AssetType: "map",
				AssetID: "map0000000001", RevisionID: "rev0000000007", Revision: 7,
			},
		}, nil
	}
	h.fetchBundle = func(context.Context, playrun.Request) (playrun.BundleResult, error) {
		return playrun.BundleResult{Ref: readyBundle(), ContentRoot: "/cache/bundle/content"}, nil
	}
	h.convert = func(context.Context, playrun.Request, string) (playrun.ConvertResult, error) {
		return playrun.ConvertResult{
			Path:      "/work/dm1.map",
			Extractor: &playrun.ExtractorRef{Version: "0.9.1", Protocol: "aue/1.2", Verified: true},
		}, nil
	}
	h.buildRun = func(_ context.Context, _ build.Request, announce func(*build.Manifest)) (*build.Manifest, error) {
		manifest := &build.Manifest{BuildID: "20260921T000000Z-abcdef", State: job.Running,
			Steps: []build.Step{{ID: "compile", JobID: "job-1", State: job.Running}}}
		announce(manifest)
		manifest.Steps[0].State = job.Succeeded
		manifest.State = job.Succeeded

		return manifest, nil
	}
	h.install = func(context.Context, playrun.Request, playrun.InstallPlan) (playrun.InstallResult, error) {
		return playrun.InstallResult{
			Dir: "/games/quake/auto-pigeon",
			Files: []playrun.StagedFile{
				{Path: "maps/dm1.bsp", SHA256: strings.Repeat("b", 64), Bytes: 1024},
				{Path: "wads/first.wad", SHA256: strings.Repeat("c", 64), Bytes: 64},
			},
		}, nil
	}
	h.verify = func(context.Context, playrun.Request, []playrun.StagedFile) error { return nil }
	h.launch = func(context.Context, playrun.Request) (playrun.LaunchRecord, error) {
		return playrun.LaunchRecord{
			ProfileID: "auto-pigeon.vkquake", ActionID: "play", JobID: "job-2",
			Executable: "/opt/vkquake/vkquake",
			Args:       []string{"-basedir", "/games/quake", "-game", "auto-pigeon", "+map", "dm1"},
		}, nil
	}

	service, err := playrun.NewService(store, playrun.Deps{
		FetchMap: func(ctx context.Context, r playrun.Request) (playrun.MapResult, error) {
			h.record("fetch-map")

			return h.fetchMap(ctx, r)
		},
		FetchBundle: func(ctx context.Context, r playrun.Request) (playrun.BundleResult, error) {
			h.record("fetch-bundle")

			return h.fetchBundle(ctx, r)
		},
		Convert: func(ctx context.Context, r playrun.Request, m string) (playrun.ConvertResult, error) {
			h.record("convert")

			return h.convert(ctx, r, m)
		},
		Build: func(ctx context.Context, r build.Request, a func(*build.Manifest)) (*build.Manifest, error) {
			h.record("build")

			return h.buildRun(ctx, r, a)
		},
		MapInputName: func(string) (string, error) { return "map_source", nil },
		PlanInstall: func(_ context.Context, record *playrun.Record) (playrun.InstallPlan, error) {
			return playrun.InstallPlan{
				BSP:  "/builds/" + record.BuildID + "/output/dm1.bsp",
				WADs: []playrun.InstallFile{{Path: "first.wad", Source: "/cache/bundle/content/first.wad"}},
			}, nil
		},
		Install: func(ctx context.Context, r playrun.Request, p playrun.InstallPlan) (playrun.InstallResult, error) {
			h.record("install")

			return h.install(ctx, r, p)
		},
		VerifyInstalled: func(ctx context.Context, r playrun.Request, f []playrun.StagedFile) error {
			h.record("verify")

			return h.verify(ctx, r, f)
		},
		Launch: func(ctx context.Context, r playrun.Request) (playrun.LaunchRecord, error) {
			h.record("launch")

			return h.launch(ctx, r)
		},
		Unstage: func(playrun.Request) error {
			h.mu.Lock()
			h.unstaged++
			h.mu.Unlock()
			h.record("unstage")

			return nil
		},
		Logf: func(format string, args ...any) { t.Logf("playrun: "+format, args...) },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.service = service

	return h
}

func (h *harness) record(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.called = append(h.called, name)
}

func (h *harness) calls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]string(nil), h.called...)
}

func readyBundle() *build.BundleRef {
	return &build.BundleRef{
		Schema: "aub-map-texture-export/1.1", MapID: "map0000000001", Revision: 7,
		Digest:       strings.Repeat("a", 64),
		WADsDeclared: []string{"first.wad", "second.wad"},
		Files: []build.BundleFile{
			{Path: "first.wad", SHA256: strings.Repeat("c", 64), Bytes: 64},
			{Path: "second.wad", SHA256: strings.Repeat("d", 64), Bytes: 64},
		},
		CompilerReady: true,
	}
}

func goodRequest() playrun.Request {
	return playrun.Request{
		AssetType: "map", AssetID: "map0000000001",
		RevisionID: "rev0000000007", RevisionNumber: 7,
		PipelineID:      "auto-pigeon.q1-normal",
		EngineProfileID: "auto-pigeon.vkquake", EngineActionID: "play",
		GameRoot: "/games/quake", MapName: "dm1",
	}
}

// await waits for a run to reach a terminal state.
func (h *harness) await(id string) *playrun.Record {
	h.t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		record, err := h.service.Get(id)
		if err != nil {
			h.t.Fatal(err)
		}
		if record.State.Terminal() {
			return record
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("run %s never finished", id)

	return nil
}

// --- the happy path -----------------------------------------------------------

func TestOneConfirmationRunsTheWholeSequence(t *testing.T) {
	h := newHarness(t)

	started, err := h.service.Start(goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	record := h.await(started.ID)

	if record.State != playrun.Succeeded {
		t.Fatalf("state = %s (%s): %s", record.State, record.FailedAt, record.Error)
	}
	want := []string{"fetch-map", "fetch-bundle", "convert", "build", "install", "verify", "launch"}
	if got := h.calls(); !equal(got, want) {
		t.Errorf("stages ran %v, want %v", got, want)
	}
	// The mod defaults to a sibling of id1, never id1.
	if record.Request.ModName != playrun.DefaultMod {
		t.Errorf("mod = %q, want %q", record.Request.ModName, playrun.DefaultMod)
	}
	// Every identity survives.
	switch {
	case record.MapSource == nil || record.MapSource.RevisionID != "rev0000000007":
		t.Errorf("map source = %+v", record.MapSource)
	case record.Bundle == nil || record.Bundle.Digest != strings.Repeat("a", 64):
		t.Errorf("bundle = %+v", record.Bundle)
	case record.Extractor == nil || !record.Extractor.Verified:
		t.Errorf("extractor = %+v", record.Extractor)
	case record.BuildID != "20260921T000000Z-abcdef":
		t.Errorf("build id = %q", record.BuildID)
	case len(record.Installed) != 2:
		t.Errorf("installed = %+v", record.Installed)
	case record.Launch == nil:
		t.Fatal("no launch was recorded")
	}
	// The argv is structured: the executable, then each argument as its own
	// element. Never a command string.
	wantArgs := []string{"-basedir", "/games/quake", "-game", "auto-pigeon", "+map", "dm1"}
	if !equal(record.Launch.Args, wantArgs) {
		t.Errorf("argv = %v, want %v", record.Launch.Args, wantArgs)
	}
	// And every stage has a duration, because a message without one is an
	// opinion.
	for _, stage := range record.Stages {
		if stage.FinishedAt.IsZero() {
			t.Errorf("the %s stage never finished", stage.State)
		}
	}
}

// A reload recovers the same run from the record alone.
func TestAReloadRecoversTheSameRun(t *testing.T) {
	h := newHarness(t)
	started, err := h.service.Start(goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	h.await(started.ID)

	// A completely separate service, over the same directory: what a restarted
	// Companion sees.
	reopened, err := playrun.OpenStore(h.store.Root())
	if err != nil {
		t.Fatal(err)
	}
	record, err := reopened.Load(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != playrun.Succeeded || record.Launch == nil {
		t.Fatalf("the reloaded record lost the run: %+v", record)
	}
	list, err := reopened.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %d records, %v", len(list), err)
	}
}

// --- the compiler-ready gate ------------------------------------------------

// A bundle AUB could not complete stops the run BEFORE the extractor or a
// compiler starts, and every named refusal reaches the record.
func TestANotCompilerReadyBundleStartsNothing(t *testing.T) {
	h := newHarness(t)
	h.fetchBundle = func(context.Context, playrun.Request) (playrun.BundleResult, error) {
		bundle := readyBundle()
		bundle.CompilerReady = false
		bundle.CompilerRefusals = []string{"wad_bytes_not_carried: quake101.wad"}

		return playrun.BundleResult{Ref: bundle, ContentRoot: "/cache/bundle/content"}, nil
	}

	started, err := h.service.Start(goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	record := h.await(started.ID)

	if record.State != playrun.Failed || record.FailedAt != playrun.DownloadingTextures {
		t.Fatalf("state = %s at %s", record.State, record.FailedAt)
	}
	if !strings.Contains(record.Error, "quake101.wad") {
		t.Errorf("error = %q, want the refusal AUB named", record.Error)
	}
	if !strings.Contains(record.Remedy, "quake101.wad") {
		t.Errorf("remedy = %q, want something the user can act on", record.Remedy)
	}
	for _, called := range h.calls() {
		if called == "convert" || called == "build" {
			t.Errorf("%s ran on a bundle that is not compiler-ready", called)
		}
	}
}

// A current export paired with a historical map revision is refused, and this
// is the one place that holds both numbers at once.
func TestABundleForAnotherRevisionIsRefused(t *testing.T) {
	h := newHarness(t)
	h.fetchBundle = func(context.Context, playrun.Request) (playrun.BundleResult, error) {
		bundle := readyBundle()
		bundle.Revision = 8

		return playrun.BundleResult{Ref: bundle, ContentRoot: "/cache/bundle/content"}, nil
	}

	started, _ := h.service.Start(goodRequest())
	record := h.await(started.ID)

	if record.State != playrun.Failed || !strings.Contains(record.Error, "revision 8") {
		t.Fatalf("state = %s, error = %q", record.State, record.Error)
	}
}

// --- failure and cleanup ------------------------------------------------------

// A failure before the install leaves nothing to clean up, and no engine is
// started.
func TestACompileFailureNeverLaunchesTheEngine(t *testing.T) {
	h := newHarness(t)
	h.buildRun = func(context.Context, build.Request, func(*build.Manifest)) (*build.Manifest, error) {
		return nil, errors.New("the compile step: leaked")
	}

	started, _ := h.service.Start(goodRequest())
	record := h.await(started.ID)

	if record.State != playrun.Failed || record.FailedAt != playrun.Compiling {
		t.Fatalf("state = %s at %s", record.State, record.FailedAt)
	}
	for _, called := range h.calls() {
		if called == "launch" {
			t.Fatal("the engine was started after a failed compile")
		}
	}
	if record.Remedy == "" {
		t.Error("a failed compile offered no next action")
	}
}

// A failure at the launch removes what was installed. A mod directory holding
// a level the engine was never started on is the half-installed state the
// contract forbids.
func TestAFailedLaunchLeavesNoHalfInstalledMod(t *testing.T) {
	h := newHarness(t)
	h.launch = func(context.Context, playrun.Request) (playrun.LaunchRecord, error) {
		return playrun.LaunchRecord{}, errors.New("the engine is not where this profile points")
	}

	started, _ := h.service.Start(goodRequest())
	record := h.await(started.ID)

	if record.State != playrun.Failed || record.FailedAt != playrun.Launching {
		t.Fatalf("state = %s at %s", record.State, record.FailedAt)
	}
	h.mu.Lock()
	unstaged := h.unstaged
	h.mu.Unlock()
	if unstaged != 1 {
		t.Errorf("unstage ran %d time(s), want once", unstaged)
	}
	if len(record.Installed) != 0 {
		t.Errorf("the record still claims %d installed file(s)", len(record.Installed))
	}
}

// A staged file that changed between installing and launching stops the launch.
func TestAChangedStagedFileStopsTheLaunch(t *testing.T) {
	h := newHarness(t)
	h.verify = func(context.Context, playrun.Request, []playrun.StagedFile) error {
		return errors.New("maps/dm1.bsp no longer hashes to what was staged")
	}

	started, _ := h.service.Start(goodRequest())
	record := h.await(started.ID)

	if record.State != playrun.Failed || record.FailedAt != playrun.Launching {
		t.Fatalf("state = %s at %s", record.State, record.FailedAt)
	}
	for _, called := range h.calls() {
		if called == "launch" {
			t.Fatal("the engine was started with a staged file that had changed")
		}
	}
}

// --- cancellation ---------------------------------------------------------------

func TestCancellingDuringTheCompileLeavesNothingInstalled(t *testing.T) {
	h := newHarness(t)
	reached := make(chan struct{})
	h.buildRun = func(ctx context.Context, _ build.Request, _ func(*build.Manifest)) (*build.Manifest, error) {
		close(reached)
		<-ctx.Done()

		return nil, ctx.Err()
	}

	started, err := h.service.Start(goodRequest())
	if err != nil {
		t.Fatal(err)
	}
	<-reached
	if err = h.service.Cancel(started.ID); err != nil {
		t.Fatal(err)
	}
	record := h.await(started.ID)

	if record.State != playrun.Cancelled {
		t.Fatalf("state = %s: %s", record.State, record.Error)
	}
	for _, called := range h.calls() {
		if called == "install" || called == "launch" {
			t.Errorf("%s ran after a cancellation", called)
		}
	}
}

func TestCancellingAfterTheInstallRemovesIt(t *testing.T) {
	h := newHarness(t)
	reached := make(chan struct{})
	h.verify = func(ctx context.Context, _ playrun.Request, _ []playrun.StagedFile) error {
		close(reached)
		<-ctx.Done()

		return ctx.Err()
	}

	started, _ := h.service.Start(goodRequest())
	<-reached
	if err := h.service.Cancel(started.ID); err != nil {
		t.Fatal(err)
	}
	record := h.await(started.ID)

	if record.State != playrun.Cancelled {
		t.Fatalf("state = %s", record.State)
	}
	h.mu.Lock()
	unstaged := h.unstaged
	h.mu.Unlock()
	if unstaged != 1 {
		t.Errorf("unstage ran %d time(s) after cancelling a run that had installed", unstaged)
	}
}

// --- retry -----------------------------------------------------------------------

// A retry is a NEW attempt linked to the previous one. Overwriting the first
// record would throw away the thing a retry most often needs.
func TestARetryIsANewAttemptLinkedToTheOld(t *testing.T) {
	h := newHarness(t)
	h.buildRun = func(context.Context, build.Request, func(*build.Manifest)) (*build.Manifest, error) {
		return nil, errors.New("leaked")
	}
	first, _ := h.service.Start(goodRequest())
	failed := h.await(first.ID)
	if failed.State != playrun.Failed {
		t.Fatalf("state = %s", failed.State)
	}

	h.buildRun = newHarness(t).buildRun
	second, err := h.service.Retry(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID {
		t.Fatal("a retry reused the first attempt's id")
	}
	retried := h.await(second.ID)
	if retried.RetryOf != first.ID {
		t.Errorf("retry_of = %q, want %q", retried.RetryOf, first.ID)
	}
	// And the first record is still exactly as it was.
	original, err := h.service.Get(first.ID)
	if err != nil || original.State != playrun.Failed {
		t.Errorf("the first attempt's record changed: %+v, %v", original, err)
	}
}

func TestASucceededRunIsNotRetried(t *testing.T) {
	h := newHarness(t)
	started, _ := h.service.Start(goodRequest())
	h.await(started.ID)

	if _, err := h.service.Retry(started.ID); err == nil {
		t.Fatal("a succeeded run was retried")
	}
}

// --- validation --------------------------------------------------------------------

func TestAnIncompletePlanIsRefusedBeforeAnythingIsDownloaded(t *testing.T) {
	h := newHarness(t)
	cases := map[string]func(*playrun.Request){
		"no map":      func(r *playrun.Request) { r.AssetID = "" },
		"no revision": func(r *playrun.Request) { r.RevisionID = "" },
		"no number":   func(r *playrun.Request) { r.RevisionNumber = 0 },
		"no pipeline": func(r *playrun.Request) { r.PipelineID = "" },
		"no engine":   func(r *playrun.Request) { r.EngineProfileID = "" },
		"no action":   func(r *playrun.Request) { r.EngineActionID = "" },
		"no game":     func(r *playrun.Request) { r.GameRoot = "" },
		"no map name": func(r *playrun.Request) { r.MapName = "" },
	}
	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			request := goodRequest()
			break_(&request)
			if _, err := h.service.Start(request); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
	if len(h.calls()) != 0 {
		t.Errorf("a refused plan still ran %v", h.calls())
	}
}

// A record left active by a stopped Companion is marked, not left claiming
// forever that a build is running.
func TestARunLeftByAStoppedCompanionIsRecovered(t *testing.T) {
	h := newHarness(t)
	reached := make(chan struct{})
	h.buildRun = func(ctx context.Context, _ build.Request, _ func(*build.Manifest)) (*build.Manifest, error) {
		close(reached)
		<-ctx.Done()

		return nil, ctx.Err()
	}
	started, _ := h.service.Start(goodRequest())
	<-reached

	// A new service over the same store, as a restarted process has.
	reopened, err := playrun.OpenStore(h.store.Root())
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := playrun.NewService(reopened, minimalDeps())
	if err != nil {
		t.Fatal(err)
	}
	count, err := fresh.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("recovered %d run(s), want 1", count)
	}
	record, err := fresh.Get(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != playrun.Failed || record.Remedy == "" {
		t.Errorf("recovered record = %s / %q", record.State, record.Remedy)
	}
}

// minimalDeps is every stage, doing nothing. For a service that only recovers.
func minimalDeps() playrun.Deps {
	return playrun.Deps{
		FetchMap: func(context.Context, playrun.Request) (playrun.MapResult, error) {
			return playrun.MapResult{}, nil
		},
		FetchBundle: func(context.Context, playrun.Request) (playrun.BundleResult, error) {
			return playrun.BundleResult{}, nil
		},
		Convert: func(context.Context, playrun.Request, string) (playrun.ConvertResult, error) {
			return playrun.ConvertResult{}, nil
		},
		Build: func(context.Context, build.Request, func(*build.Manifest)) (*build.Manifest, error) {
			return nil, nil
		},
		MapInputName: func(string) (string, error) { return "map_source", nil },
		PlanInstall: func(context.Context, *playrun.Record) (playrun.InstallPlan, error) {
			return playrun.InstallPlan{}, nil
		},
		Install: func(context.Context, playrun.Request, playrun.InstallPlan) (playrun.InstallResult, error) {
			return playrun.InstallResult{}, nil
		},
		Launch: func(context.Context, playrun.Request) (playrun.LaunchRecord, error) {
			return playrun.LaunchRecord{}, nil
		},
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
