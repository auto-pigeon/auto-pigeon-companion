package playrun_test

import (
	"context"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/build"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/playrun"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// compiled is a successful build whose compile step classified these lines.
func compiled(diagnostics ...job.Diagnostic) func(context.Context, build.Request, func(*build.Manifest)) (*build.Manifest, error) {
	return func(_ context.Context, _ build.Request, announce func(*build.Manifest)) (*build.Manifest, error) {
		manifest := &build.Manifest{BuildID: "20260923T000000Z-abcdef", State: job.Succeeded,
			Steps: []build.Step{{ID: "compile", JobID: "job-1", State: job.Succeeded, Diagnostics: diagnostics}}}
		announce(manifest)

		return manifest, nil
	}
}

func line(rule, raw string) job.Diagnostic {
	return job.Diagnostic{RuleID: rule, Severity: profile.SeverityWarning, Stream: "stdout", Raw: raw}
}

// The measured dm2 compile: no WAD opened, every texture missing, exit 0. It
// must not reach the game.
func TestABuildThatOpenedNoWADFails(t *testing.T) {
	h := newHarness(t)
	h.buildRun = compiled(
		line("wad_not_found", "addArchive: WARNING: archive 'gfx/metal.wad' not found"),
		line("no_valid_wad", "WARNING: No valid WAD filenames in worldmodel"),
		line("texture_not_found", "WARNING: unable to find texture skip"),
		line("texture_not_found", "WARNING: unable to find texture SKY4"),
		line("texture_not_found", "WARNING: unable to find texture METAL4_4"),
	)

	started, _ := h.service.Start(goodRequest())
	record := h.await(started.ID)

	if record.State != playrun.Failed || record.FailedAt != playrun.Compiling {
		t.Fatalf("state = %s at %s, want failed at compiling", record.State, record.FailedAt)
	}
	if !strings.Contains(record.Error, "none of the map's textures") || !strings.Contains(record.Error, "SKY4") {
		t.Errorf("error = %q, want it to say no textures and name them", record.Error)
	}
	if strings.Contains(record.Error, "skip") {
		t.Errorf("error = %q, want the skip tool texture left out", record.Error)
	}
	if record.Remedy == "" {
		t.Error("a build with no textures offered no next action")
	}
	for _, called := range h.calls() {
		if called == "launch" || called == "install" {
			t.Fatalf("%s ran after a build with no textures", called)
		}
	}
}

// Some textures missing: the level is playable, and the run says which.
func TestABuildMissingSomeTexturesWarnsAndNamesThem(t *testing.T) {
	h := newHarness(t)
	h.buildRun = compiled(
		line("texture_not_found", "WARNING: unable to find texture skip"),
		line("texture_not_found", "WARNING: unable to find texture CITY4_2"),
	)

	started, _ := h.service.Start(goodRequest())
	record := h.await(started.ID)

	if record.State != playrun.Succeeded {
		t.Fatalf("state = %s (%s), want succeeded", record.State, record.Error)
	}
	if len(record.Warnings) != 1 || !strings.Contains(record.Warnings[0], "CITY4_2") ||
		!strings.Contains(record.Warnings[0], "1 texture") {
		t.Fatalf("warnings = %q, want one naming CITY4_2", record.Warnings)
	}
}

// Only a tool texture missing is the normal case, and says nothing.
func TestOnlyAToolTextureMissingIsNotAWarning(t *testing.T) {
	h := newHarness(t)
	h.buildRun = compiled(line("texture_not_found", "WARNING: unable to find texture skip"))

	started, _ := h.service.Start(goodRequest())
	record := h.await(started.ID)

	if record.State != playrun.Succeeded || len(record.Warnings) != 0 {
		t.Fatalf("state = %s, warnings = %q; want succeeded with none", record.State, record.Warnings)
	}
}
