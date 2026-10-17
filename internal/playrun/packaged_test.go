package playrun_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2pack"
)

// Quake II through Build & Run (Q2_020). Q2_009 refused it and pointed at the
// package panel; the sequence now runs the panel's own owners — internal/q2pack,
// q2install and q2run, behind Deps — in one record. These tests hold the order
// and the record; the owners have their own.

func quake2Request() playrun.Request {
	return playrun.Request{
		AssetType: "map", AssetID: "map0000000001",
		RevisionID: "rev0000000007", RevisionNumber: 7,
		PipelineID:      "auto-pigeon.ericw.q2-normal",
		EngineProfileID: "auto-pigeon.engine.yamagi-quake2", EngineActionID: "play_map",
		GameRoot: "/games/Quake II", MapName: "q2room", Family: q2pack.FamilyQuake2,
		Grants: []q2pack.Grant{{Basis: q2pack.BasisOwnWork, Loose: true}},
	}
}

// packagedHarness is the harness with a Quake II install and launch.
func packagedHarness(t *testing.T) *harness {
	h := newHarness(t)
	h.install = func(_ context.Context, request playrun.Request, plan playrun.InstallPlan) (playrun.InstallResult, error) {
		return playrun.InstallResult{
			Dir:          "/games/Quake II/" + request.ModName,
			Package:      &playrun.PackageRef{ID: "q2-q2room-0123", Archive: "q2room.pak", SHA256: strings.Repeat("e", 64), Members: 5, Complete: true},
			Installation: &playrun.InstallationRef{ID: "inst-1", ModDir: "/games/Quake II/" + request.ModName, FSGame: request.ModName, MapName: "q2room"},
		}, nil
	}
	h.launch = func(context.Context, playrun.Request) (playrun.LaunchRecord, error) {
		return playrun.LaunchRecord{
			ProfileID: "auto-pigeon.engine.yamagi-quake2", ActionID: "play_map", JobID: "job-q2",
			Executable: "/opt/yamagi/quake2", Args: []string{"+set", "game", "aup-q2room", "+map", "q2room"},
			MapLoad: "accepted", LoadedFrom: "/games/Quake II/aup-q2room/q2room.pak", SourceVerified: true,
			Message: "The engine loaded the map. It opened it from the installed package.",
		}, nil
	}
	return h
}

func TestAQuake2RunPackagesInstallsAndWaitsForTheEngine(t *testing.T) {
	h := packagedHarness(t)
	var roots map[string]string
	var rootsGiven bool
	h.buildRun = func(_ context.Context, request build.Request, announce func(*build.Manifest)) (*build.Manifest, error) {
		roots, rootsGiven = request.Roots, request.RootSources != nil
		manifest := &build.Manifest{BuildID: "20261017T000000Z-q2q2q2", State: job.Succeeded}
		announce(manifest)
		return manifest, nil
	}
	h.deps.DeclaresRoot = func(string, string) (bool, error) {
		t.Error("a Quake II run asked whether its pipeline reads a WAD folder")
		return true, nil
	}
	service, err := playrun.NewService(h.store, h.deps)
	if err != nil {
		t.Fatal(err)
	}
	h.service = service
	started, err := h.service.Start(quake2Request())
	if err != nil {
		t.Fatal(err)
	}
	record := h.await(started.ID)
	if record.State != playrun.Succeeded {
		t.Fatalf("state = %s at %s: %s", record.State, record.FailedAt, record.Error)
	}
	// No texture bundle: a Quake II compiler reads its bound game folders.
	want := []string{"fetch-map", "convert", "build", "install", "verify", "launch"}
	if got := h.calls(); !equal(got, want) {
		t.Errorf("stages ran %v, want %v", got, want)
	}
	for _, stage := range record.Stages {
		if stage.State == playrun.DownloadingTextures {
			t.Error("the record has a texture-download stage")
		}
	}
	if len(roots) != 0 || rootsGiven {
		t.Errorf("the build was handed roots %v; it must read the bound game folders", roots)
	}
	// The package panel's default mod folder, one per map.
	if record.Request.ModName != "aup-q2room" {
		t.Errorf("mod = %q, want aup-q2room", record.Request.ModName)
	}
	switch {
	case record.Package == nil || record.Package.Archive != "q2room.pak":
		t.Errorf("package = %+v", record.Package)
	case record.Installation == nil || record.Installation.FSGame != "aup-q2room":
		t.Errorf("installation = %+v", record.Installation)
	case record.Launch == nil || record.Launch.MapLoad != "accepted" || !record.Launch.SourceVerified:
		t.Errorf("launch = %+v", record.Launch)
	}
	if len(record.Request.Grants) != 1 || record.Request.Grants[0].Basis != q2pack.BasisOwnWork {
		t.Errorf("grants were not recorded for a retry: %+v", record.Request.Grants)
	}
}

// The package plan names something that may not travel: nothing is written,
// the run fails at Installing with the plan's own words, and its remedy.
func TestAHeldQuake2PackageFailsWithThePlansReason(t *testing.T) {
	h := packagedHarness(t)
	h.deps.PlanInstall = func(context.Context, *playrun.Record) (playrun.InstallPlan, error) {
		return playrun.InstallPlan{}, &playrun.RemedyError{
			Err:    errors.New("playrun: the package is held: textures/q2020/crate.wal: nothing holds it"),
			Remedy: "Put crate.wal in the content folder bound to the compiler, then press Try again.",
		}
	}
	service, err := playrun.NewService(h.store, h.deps)
	if err != nil {
		t.Fatal(err)
	}
	h.service = service
	started, _ := h.service.Start(quake2Request())
	record := h.await(started.ID)
	if record.State != playrun.Failed || record.FailedAt != playrun.Installing {
		t.Fatalf("state = %s at %s", record.State, record.FailedAt)
	}
	if !strings.Contains(record.Error, "crate.wal") || !strings.Contains(record.Remedy, "Try again") {
		t.Errorf("error %q, remedy %q", record.Error, record.Remedy)
	}
	for _, call := range h.calls() {
		if call == "install" || call == "launch" {
			t.Errorf("%s ran after a held package", call)
		}
	}
	// Retry is the ordinary one, with the same request.
	again, err := h.service.Retry(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Request.Family != q2pack.FamilyQuake2 || again.RetryOf != record.ID {
		t.Errorf("retry = %+v", again.Request)
	}
	h.await(again.ID)
}

// The engine refused the map: the run fails at Launching, keeps what started
// (the job and its argv), takes the engine's hint as its remedy, and leaves the
// complete installation where it is.
func TestAQuake2EngineThatRefusesTheMapFailsTheRunAndKeepsTheInstall(t *testing.T) {
	h := packagedHarness(t)
	h.launch = func(context.Context, playrun.Request) (playrun.LaunchRecord, error) {
		launched := playrun.LaunchRecord{ProfileID: "auto-pigeon.engine.yamagi-quake2", ActionID: "play_map",
			JobID: "job-q2", Executable: "/opt/yamagi/quake2", MapLoad: "refused",
			Message: "The engine found no renderer it could load."}
		return launched, &playrun.RemedyError{Err: errors.New(launched.Message), Remedy: "Install a renderer beside the engine."}
	}
	service, err := playrun.NewService(h.store, h.deps)
	if err != nil {
		t.Fatal(err)
	}
	h.service = service
	started, _ := h.service.Start(quake2Request())
	record := h.await(started.ID)
	if record.State != playrun.Failed || record.FailedAt != playrun.Launching {
		t.Fatalf("state = %s at %s", record.State, record.FailedAt)
	}
	if record.Launch == nil || record.Launch.JobID != "job-q2" || record.Launch.MapLoad != "refused" {
		t.Errorf("launch = %+v", record.Launch)
	}
	if record.Remedy != "Install a renderer beside the engine." {
		t.Errorf("remedy = %q", record.Remedy)
	}
	h.mu.Lock()
	unstaged := h.unstaged
	h.mu.Unlock()
	if unstaged != 0 || record.Installation == nil {
		t.Errorf("unstaged %d time(s), installation %+v: a complete package install is kept", unstaged, record.Installation)
	}
}

// Cancelling while the engine is being waited for stops the run as cancelled.
func TestCancellingAQuake2RunWhileTheEngineLoadsIsACancel(t *testing.T) {
	h := packagedHarness(t)
	waiting := make(chan struct{})
	h.launch = func(ctx context.Context, _ playrun.Request) (playrun.LaunchRecord, error) {
		close(waiting)
		<-ctx.Done()
		return playrun.LaunchRecord{JobID: "job-q2", MapLoad: "refused", Message: "The run was cancelled before the engine reported loading the map."},
			errors.New("the run was cancelled before the engine reported loading the map")
	}
	service, err := playrun.NewService(h.store, h.deps)
	if err != nil {
		t.Fatal(err)
	}
	h.service = service
	started, _ := h.service.Start(quake2Request())
	<-waiting
	if err := h.service.Cancel(started.ID); err != nil {
		t.Fatal(err)
	}
	record := h.await(started.ID)
	if record.State != playrun.Cancelled {
		t.Fatalf("state = %s (%s)", record.State, record.Error)
	}
}

func TestAQuake2RequestIsCheckedBeforeAnythingRuns(t *testing.T) {
	for name, change := range map[string]func(*playrun.Request){
		"own WADs":            func(r *playrun.Request) { r.OwnWADsDir = "/home/me/wads" },
		"a hosted listing":    func(r *playrun.Request) { r.Listing = &playrun.Listing{Title: "x"} },
		"grants on a Quake 1": func(r *playrun.Request) { r.Family = "" },
	} {
		request := quake2Request()
		change(&request)
		if err := request.Normalize(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	named := quake2Request()
	named.ModName = "my-mod"
	if err := named.Normalize(); err != nil || named.ModName != "my-mod" {
		t.Errorf("a named mod folder: %v %q", err, named.ModName)
	}
}
