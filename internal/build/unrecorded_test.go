package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
)

// blockManifestWrites makes every later Save of this build fail, the way a
// full or read-only disk does: the name Save writes through is taken by a
// directory. Called from Announce, which is the runner telling its caller a
// manifest was just written.
func blockManifestWrites(t *testing.T, manifest *Manifest) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(manifest.Directory, ManifestFileName+".writing"), 0o700); err != nil {
		t.Errorf("blocking the manifest: %v", err)
	}
}

// A build whose compilers all exited 0 and whose manifest could not be written
// did not succeed: nothing that reads the build later can see that it did. It
// used to be returned as `succeeded` beside the error (NEW_323B).
func TestABuildWhoseOutcomeCannotBeWrittenIsNotReturnedAsSucceeded(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	h.runner.options.Announce = func(manifest *Manifest) {
		for _, step := range manifest.Steps {
			if step.State != job.Succeeded {
				return
			}
		}
		blockManifestWrites(t, manifest)
	}
	manifest, err := h.run(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	if err == nil {
		t.Fatal("a build whose manifest could not be written returned no error")
	}
	if manifest == nil {
		t.Fatalf("the build returned no manifest at all: %v", err)
	}
	if manifest.State != job.Failed || !strings.Contains(manifest.Error, ManifestFileName) {
		t.Fatalf("the returned manifest says %s (%q); want failed, naming the file that could not be written", manifest.State, manifest.Error)
	}
	if manifest.Error != err.Error() {
		t.Errorf("the manifest records %q and Run returned %q", manifest.Error, err)
	}
	for _, step := range manifest.Steps {
		if step.State != job.Succeeded {
			t.Errorf("the %s stage ran to the end and is recorded %s", step.ID, step.State)
		}
	}
	// The file is what it was: the last manifest that could be written.
	saved, loadErr := LoadManifest(filepath.Join(manifest.Directory, ManifestFileName))
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if saved.State == job.Succeeded {
		t.Fatal("the manifest on disk says succeeded; the write that would have said so failed")
	}
}

// Part-way through, the same failure used to return NO manifest: a caller
// watching the build had a run that was over and no document saying what it
// had been.
func TestABuildWhoseManifestCannotBeWrittenPartWayReturnsItsManifestFailed(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	blocked := false
	h.runner.options.Announce = func(manifest *Manifest) {
		if !blocked && len(manifest.Steps) > 0 && manifest.Steps[0].JobID != "" {
			blocked = true
			blockManifestWrites(t, manifest)
		}
	}
	manifest, err := h.run(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("level.map", "brushes\n")},
	})
	if err == nil || manifest == nil {
		t.Fatalf("manifest = %v, err = %v; want the manifest and the write's error", manifest, err)
	}
	if manifest.State != job.Failed || !strings.Contains(manifest.Error, "recording the build") {
		t.Fatalf("the returned manifest says %s (%q)", manifest.State, manifest.Error)
	}
	if manifest.FinishedAt.IsZero() {
		t.Error("a failed build with no finishing time")
	}
	if len(manifest.Steps) != 3 || !manifest.Steps[2].Skipped {
		t.Errorf("the stages after the one that could not be recorded are not recorded as skipped: %+v", manifest.Steps)
	}
}
