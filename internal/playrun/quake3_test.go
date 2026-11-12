package playrun_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3pack"
)

// Quake III through the saved-map run (NEW_504, AUCOM #28). The run builds the
// exact revision with the Q3Map2 stages and ends at the BSP: packaging,
// installing and starting an engine are separate actions under the build's
// result, so a Quake III run that would do them is refused before anything is
// downloaded.

func quake3BuildOnly() playrun.Request {
	return playrun.Request{
		AssetType: "map", AssetID: "map0000000001",
		RevisionID: "rev0000000007", RevisionNumber: 7,
		PipelineID: "auto-pigeon.q3.normal", Family: q3pack.FamilyQuake3, BuildOnly: true,
	}
}

func TestAQuake3RunBuildsFromTheBoundFoldersAndEndsAtTheBSP(t *testing.T) {
	h := newHarness(t)
	var given build.Request
	h.buildRun = func(_ context.Context, request build.Request, announce func(*build.Manifest)) (*build.Manifest, error) {
		given = request
		manifest := &build.Manifest{BuildID: "20261112T000000Z-q3q3q3", State: job.Succeeded,
			Steps: []build.Step{{ID: "compile", State: job.Succeeded, Diagnostics: []job.Diagnostic{
				{RuleID: "missing_image", Raw: "WARNING: Couldn't find image for shader textures/aup/crate"},
			}}}}
		announce(manifest)
		return manifest, nil
	}
	h.deps.DeclaresRoot = func(string, string) (bool, error) {
		t.Error("a Quake III run asked whether its pipeline reads a WAD folder")
		return true, nil
	}
	service, err := playrun.NewService(h.store, h.deps)
	if err != nil {
		t.Fatal(err)
	}
	started, err := service.Start(quake3BuildOnly())
	if err != nil {
		t.Fatal(err)
	}
	h.service = service
	record := h.await(started.ID)
	if record.State != playrun.Succeeded {
		t.Fatalf("state = %s at %s: %s (%s)", record.State, record.FailedAt, record.Error, record.Remedy)
	}
	// AUB holds no Quake III texture source: no export is asked for, and
	// nothing is installed or started.
	if got, want := h.calls(), []string{"fetch-map", "convert", "build"}; !equal(got, want) {
		t.Errorf("stages ran %v, want %v", got, want)
	}
	if len(given.Roots) != 0 || given.RootSources != nil || given.Q2Textures != nil {
		t.Errorf("the build was handed roots %v / %v; it must read the folders bound to Q3Map2", given.Roots, given.Q2Textures)
	}
	if given.PipelineID != "auto-pigeon.q3.normal" || given.Inputs["map_source"] != "/work/dm1.map" {
		t.Errorf("the build request %+v", given)
	}
	textures := record.Stages[1]
	if textures.State != playrun.DownloadingTextures || !strings.Contains(textures.Detail, "no Quake III texture sources") {
		t.Errorf("the texture stage says %+v", textures)
	}
	if record.Bundle != nil || len(record.Installed) != 0 || record.Launch != nil {
		t.Errorf("a Quake III build recorded a bundle, an install or a launch: %+v", record)
	}
	if len(record.Warnings) != 1 || !strings.Contains(record.Warnings[0], "textures/aup/crate") ||
		!strings.Contains(record.Warnings[0], "game folders bound to the compiler") {
		t.Errorf("warnings %v", record.Warnings)
	}
	if record.BuildID != "20261112T000000Z-q3q3q3" {
		t.Errorf("build id %q", record.BuildID)
	}
}

func TestAQuake3RunThatWouldInstallOrStartAGameIsRefusedFirst(t *testing.T) {
	h := newHarness(t)
	request := quake3BuildOnly()
	request.BuildOnly = false
	request.EngineProfileID, request.EngineActionID = "auto-pigeon.engine.ioquake3", "play_map"
	request.GameRoot, request.MapName = "/games/quake3", "room"
	if _, err := h.service.Start(request); !errors.Is(err, playrun.ErrQuake3BuildsOnly) {
		t.Fatalf("a Quake III run with an engine started: %v", err)
	}
	own := quake3BuildOnly()
	own.OwnWADsDir = "/home/me/wads"
	if _, err := h.service.Start(own); err == nil || !strings.Contains(err.Error(), "no WADs to supply") {
		t.Fatalf("a Quake III run took a WAD folder: %v", err)
	}
	if got := h.calls(); len(got) != 0 {
		t.Fatalf("a refused run ran %v", got)
	}
}
