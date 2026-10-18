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

// q2Bundle is a Quake II export as AUB writes one (seen live, Q2_015A): one
// account texture source the map binds, carried; one namespace installed with
// the game, named and not sent — so AUB calls it not compiler-ready.
func q2Bundle(status string, staged bool) playrun.BundleResult {
	ref := &build.BundleRef{
		Schema: "aub-map-texture-export/1.2", MapID: "map0000000001", Revision: 7,
		Digest: strings.Repeat("b", 64),
		Files: []build.BundleFile{
			{Path: "aup687697486/red_7a099681.wal", Source: "aup687697486", SHA256: strings.Repeat("1", 64), Bytes: 10980},
			{Path: "aup687697486/blue_119bf034.wal", Source: "aup687697486", SHA256: strings.Repeat("2", 64), Bytes: 10980},
		},
		CompilerReady:    false,
		CompilerRefusals: []string{"wad_inventory_incomplete", "wad_bytes_not_carried: e1u1"},
		Bound: []build.BoundTextureSource{{Name: "aup687697486", Status: status, SourceID: "ldksgssnnrdjvb3",
			RevisionID: "sufmmp7nard130t", Revision: 2}},
	}
	if !staged {
		ref.Files = nil
	}
	binding := ref.Bound[0]
	return playrun.BundleResult{Ref: ref, ContentRoot: "/cache/q2bundle/content", Licenses: "/cache/q2bundle/LICENSES.md",
		Sources: []playrun.WADSource{
			{Name: "aup687697486", Origin: "user", Staged: staged, Revision: 2, Binding: &binding},
			{Name: "e1u1", Origin: "installed", Staged: false, NotSentReason: "undeclared"},
		}}
}

func TestAQuake2RunPackagesInstallsAndWaitsForTheEngine(t *testing.T) {
	h := packagedHarness(t)
	h.fetchBundle = func(context.Context, playrun.Request) (playrun.BundleResult, error) {
		return q2Bundle("bound", true), nil
	}
	var given build.Request
	h.buildRun = func(_ context.Context, request build.Request, announce func(*build.Manifest)) (*build.Manifest, error) {
		given = request
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
		t.Fatalf("state = %s at %s: %s (%s)", record.State, record.FailedAt, record.Error, record.Remedy)
	}
	// The texture export IS downloaded (Q2_015A), and AUB's compiler_ready —
	// false, for the base game's e1u1 it never sends — does not stop the run.
	want := []string{"fetch-map", "fetch-bundle", "convert", "build", "install", "verify", "launch"}
	if got := h.calls(); !equal(got, want) {
		t.Errorf("stages ran %v, want %v", got, want)
	}
	// The build reads the bound game folders, and is given the export to lay
	// over the content folder: every carried file, the export's identity and
	// its licence summary.
	if len(given.Roots) != 0 || given.RootSources != nil {
		t.Errorf("the build was handed roots %v; it must read the bound game folders", given.Roots)
	}
	textures := given.Q2Textures
	switch {
	case textures == nil:
		t.Fatal("the build was not given the map's account textures")
	case textures.Dir != "/cache/q2bundle/content" || len(textures.Files) != 2 ||
		textures.Bundle == nil || textures.Bundle.Digest != strings.Repeat("b", 64) ||
		textures.Licenses != "/cache/q2bundle/LICENSES.md":
		t.Errorf("account textures = %+v", textures)
	}
	if record.Bundle == nil || len(record.Bundle.Bound) != 1 || record.Bundle.Bound[0].Revision != 2 {
		t.Errorf("the record does not say which revision the map binds: %+v", record.Bundle)
	}
	for _, stage := range record.Stages {
		if stage.State == playrun.DownloadingTextures &&
			(!strings.Contains(stage.Detail, "aup687697486 revision 2") || !strings.Contains(stage.Detail, "e1u1")) {
			t.Errorf("the texture stage says %q", stage.Detail)
		}
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

// What stops a Quake II run at its textures, before anything compiles: a bound
// revision that is not available, and a bound source whose files did not
// arrive verified. Each names the source and says what to do.
func TestAQuake2RunStopsWhenABoundTextureSourceIsNotUsable(t *testing.T) {
	for name, tc := range map[string]struct {
		bundle        playrun.BundleResult
		error, remedy string
	}{
		"binding unavailable": {q2Bundle("bound_revision_unavailable", true),
			"revision 2, which is not available (bound_revision_unavailable)", "LOAD › Textures"},
		"files not verified": {q2Bundle("bound", false),
			"the files of the texture source aup687697486 are not verified", "signed in"},
	} {
		t.Run(name, func(t *testing.T) {
			h := packagedHarness(t)
			h.fetchBundle = func(context.Context, playrun.Request) (playrun.BundleResult, error) { return tc.bundle, nil }
			service, err := playrun.NewService(h.store, h.deps)
			if err != nil {
				t.Fatal(err)
			}
			h.service = service
			started, _ := h.service.Start(quake2Request())
			record := h.await(started.ID)
			if record.State != playrun.Failed || record.FailedAt != playrun.DownloadingTextures {
				t.Fatalf("state = %s at %s", record.State, record.FailedAt)
			}
			if !strings.Contains(record.Error, tc.error) || !strings.Contains(record.Remedy, tc.remedy) ||
				!strings.Contains(record.Remedy, "aup687697486") {
				t.Errorf("error %q, remedy %q", record.Error, record.Remedy)
			}
			for _, call := range h.calls() {
				if call == "convert" || call == "build" {
					t.Errorf("%s ran after the texture gate refused", call)
				}
			}
		})
	}
}

// A compiler that did not find a texture the export carried is a failed
// build, not a placeholder: the files were laid where it reads them.
func TestAQuake2BuildThatMissesAnAccountTextureFails(t *testing.T) {
	h := packagedHarness(t)
	h.fetchBundle = func(context.Context, playrun.Request) (playrun.BundleResult, error) {
		return q2Bundle("bound", true), nil
	}
	h.buildRun = func(_ context.Context, _ build.Request, announce func(*build.Manifest)) (*build.Manifest, error) {
		manifest := &build.Manifest{BuildID: "20261017T000000Z-q2miss", State: job.Succeeded, Steps: []build.Step{{
			ID: "compile", State: job.Succeeded, Diagnostics: []job.Diagnostic{
				{RuleID: "texture_missing", Raw: "WARNING: Couldn't locate texture for e1u1/metal1_1"},
				{RuleID: "texture_missing", Raw: "WARNING: Couldn't locate texture for aup687697486/red_7a099681"},
			}}}}
		announce(manifest)
		return manifest, nil
	}
	service, err := playrun.NewService(h.store, h.deps)
	if err != nil {
		t.Fatal(err)
	}
	h.service = service
	started, _ := h.service.Start(quake2Request())
	record := h.await(started.ID)
	if record.State != playrun.Failed || record.FailedAt != playrun.Compiling ||
		!strings.Contains(record.Error, "aup687697486/red_7a099681") || strings.Contains(record.Error, "e1u1") {
		t.Fatalf("state = %s at %s: %s", record.State, record.FailedAt, record.Error)
	}
}

// Cancelled while the export downloads: the run is cancelled there, nothing
// compiles, and the record holds no texture root — a half-downloaded export is
// never something a build reads (internal/texturebundle publishes an entry
// whole or not at all).
func TestCancellingAQuake2RunWhileItsTexturesDownload(t *testing.T) {
	h := packagedHarness(t)
	downloading := make(chan struct{})
	h.fetchBundle = func(ctx context.Context, _ playrun.Request) (playrun.BundleResult, error) {
		close(downloading)
		<-ctx.Done()
		return playrun.BundleResult{}, ctx.Err()
	}
	service, err := playrun.NewService(h.store, h.deps)
	if err != nil {
		t.Fatal(err)
	}
	h.service = service
	started, _ := h.service.Start(quake2Request())
	<-downloading
	if err := h.service.Cancel(started.ID); err != nil {
		t.Fatal(err)
	}
	record := h.await(started.ID)
	if record.State != playrun.Cancelled || record.BundleRoot != "" || record.Bundle != nil {
		t.Fatalf("state = %s, bundle root %q, bundle %+v", record.State, record.BundleRoot, record.Bundle)
	}
	for _, call := range h.calls() {
		if call == "convert" || call == "build" {
			t.Errorf("%s ran after a cancel", call)
		}
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
