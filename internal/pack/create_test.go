package pack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fixedTime keeps a manifest assertable without excluding one field from it.
func fixedTime() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) }

// planFrom is the whole selection path, for the tests that are about what
// happens after it.
func planFrom(t *testing.T, root string, target Target, policy Policy) *Plan {
	t.Helper()
	candidates, err := Collect([]DirSource{{Dir: root}}, nil, target)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if policy.AuthoredRoots == nil && policy.GameRoots == nil && policy.BuildOutputs == nil {
		policy.AuthoredRoots = []string{root}
	}
	plan, err := NewPlan(candidates, policy, target)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	return plan
}

func TestCreateWritesAnArchiveAndASidecar(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "maps/e1m1.bsp", "the compiled level")
	writeFile(t, root, "gfx/palette.lmp", "palette")
	out := filepath.Join(t.TempDir(), "mymap.pak")

	plan := planFrom(t, root, mustTarget(t, "quake-pak"), Policy{})
	result, err := Create(plan, Options{
		Output: out, Companion: "1.2.3", Label: "my map", Now: fixedTime,
		Build: &BuildRef{BuildID: "b1", Pipeline: DocumentRef{ID: "aucom.pipeline.q1"}, ReproducibleKey: "sha256:abc"},
		Tools: []ToolRef{{Profile: DocumentRef{ID: "aucom.tool.qbsp", Version: "0.18.1"}, ToolVersion: "0.18.1"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if result.ManifestPath != out+ManifestSuffix {
		t.Fatalf("manifest is at %s, want %s", result.ManifestPath, out+ManifestSuffix)
	}

	manifest, err := LoadManifest(result.ManifestPath)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if manifest.Archive.Entries != 2 || manifest.Archive.File != "mymap.pak" {
		t.Fatalf("manifest describes %+v", manifest.Archive)
	}
	if manifest.Target.Reproducible != Portable {
		t.Fatalf("a PAK's manifest claims %q", manifest.Target.Reproducible)
	}
	if manifest.Build == nil || manifest.Build.ReproducibleKey != "sha256:abc" {
		t.Fatalf("the build reference did not survive: %+v", manifest.Build)
	}
	if len(manifest.Tools) != 1 || manifest.Tools[0].ToolVersion != "0.18.1" {
		t.Fatalf("the tool versions did not survive: %+v", manifest.Tools)
	}
	// Every member carries its digest and the decision that let it in.
	for _, entry := range manifest.Contents {
		if entry.SHA256 == "" || entry.Rule == "" || entry.Reason == "" {
			t.Fatalf("%s is recorded as %+v", entry.Path, entry)
		}
	}
	// And the sidecar agrees with the archive it describes.
	verification, err := Verify(out, FormatPAK, Budget{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verification.ManifestPath == "" {
		t.Fatal("Verify did not find the sidecar beside the archive")
	}
	if !verification.ManifestAgrees {
		t.Fatalf("the sidecar disagrees with its own archive: %v", verification.ManifestIssues)
	}
}

func TestManifestNoticesATamperedArchive(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "aaaa")
	writeFile(t, root, "b.txt", "bbbb")
	out := filepath.Join(t.TempDir(), "x.pak")
	plan := planFrom(t, root, mustTarget(t, "quake-pak"), Policy{})
	if _, err := Create(plan, Options{Output: out, Now: fixedTime}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Repack with one member changed, keeping the sidecar from the first run.
	writeFile(t, root, "b.txt", "cccc")
	second := planFrom(t, root, mustTarget(t, "quake-pak"), Policy{})
	repacked := filepath.Join(t.TempDir(), "x.pak")
	if _, err := Create(second, Options{Output: repacked, Now: fixedTime}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.Rename(out+ManifestSuffix, repacked+ManifestSuffix); err != nil {
		t.Fatalf("moving the sidecar: %v", err)
	}
	verification, err := Verify(repacked, FormatPAK, Budget{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verification.ManifestAgrees {
		t.Fatal("a sidecar from a different archive was accepted as describing this one")
	}
	joined := strings.Join(verification.ManifestIssues, " | ")
	if !strings.Contains(joined, "digest") {
		t.Fatalf("issues are %q, want the digest mismatch named", joined)
	}
}

func TestCreateRefusesToOverwriteItsOwnInput(t *testing.T) {
	// The ordinary accident: package a directory into itself, twice.
	root := t.TempDir()
	writeFile(t, root, "maps/e1m1.bsp", "level")
	out := filepath.Join(root, "mymap.pak")

	plan := planFrom(t, root, mustTarget(t, "quake-pak"), Policy{})
	if _, err := Create(plan, Options{Output: out, Now: fixedTime}); err != nil {
		t.Fatalf("the first run failed: %v", err)
	}
	// The second sweep now picks up the archive itself.
	second := planFrom(t, root, mustTarget(t, "quake-pak"), Policy{})
	_, err := Create(second, Options{Output: out, Replace: true, Now: fixedTime})
	if err == nil {
		t.Fatal("an archive was written over a file it was about to read")
	}
	if !errors.Is(err, ErrWouldOverwrite) {
		t.Fatalf("error is %v, want an ErrWouldOverwrite", err)
	}
}

func TestCreateRefusesAnExistingDestinationWithoutReplace(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "a")
	out := filepath.Join(t.TempDir(), "x.pak")

	plan := planFrom(t, root, mustTarget(t, "quake-pak"), Policy{})
	if _, err := Create(plan, Options{Output: out, Now: fixedTime}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	again := planFrom(t, root, mustTarget(t, "quake-pak"), Policy{})
	_, err := Create(again, Options{Output: out, Now: fixedTime})
	if !errors.Is(err, ErrWouldOverwrite) {
		t.Fatalf("error is %v, want an ErrWouldOverwrite", err)
	}
	if !strings.Contains(err.Error(), "--replace") {
		t.Fatalf("error is %v, want it to say what to do about it", err)
	}
	if _, err := Create(again, Options{Output: out, Replace: true, Now: fixedTime}); err != nil {
		t.Fatalf("--replace was refused too: %v", err)
	}
}

func TestCreateLeavesNothingBehindWhenItFails(t *testing.T) {
	root := t.TempDir()
	source := writeFile(t, root, "a.txt", "the reviewed bytes")
	destination := t.TempDir()
	target := mustTarget(t, "quake-pak")

	candidates, err := Collect([]DirSource{{Dir: root}}, nil, target)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	plan, err := NewPlan(candidates, Policy{AuthoredRoots: []string{root}}, target)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	// Change the file after the plan recorded it: the write refuses halfway.
	if err := os.WriteFile(source, []byte("different, longer bytes than were reviewed"), 0o644); err != nil {
		t.Fatalf("rewriting: %v", err)
	}
	out := filepath.Join(destination, "x.pak")
	if _, err := Create(plan, Options{Output: out, Now: fixedTime}); err == nil {
		t.Fatal("a changed source was packaged")
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the destination exists after a failed write: %v", err)
	}
	left, err := os.ReadDir(destination)
	if err != nil {
		t.Fatalf("reading the destination: %v", err)
	}
	if len(left) != 0 {
		names := make([]string, len(left))
		for i, entry := range left {
			names[i] = entry.Name()
		}
		t.Fatalf("a failed write left %v behind", names)
	}
}

func TestCreateRefusesAPlanWithUnreviewedFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "maps/e1m1.bsp", "level")
	writeFile(t, root, "gfx/palette.lmp", "palette")
	target := mustTarget(t, "quake-pak")

	// Nothing declared: both files are unknown, both are held.
	candidates, err := Collect([]DirSource{{Dir: root}}, nil, target)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	plan, err := NewPlan(candidates, Policy{}, target)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	if len(plan.NeedsReview()) != 2 {
		t.Fatalf("%d files need review, want 2", len(plan.NeedsReview()))
	}
	out := filepath.Join(t.TempDir(), "x.pak")
	_, err = Create(plan, Options{Output: out, Now: fixedTime})
	if err == nil {
		t.Fatal("a plan with unreviewed files was written")
	}
	if !strings.Contains(err.Error(), "need review") {
		t.Fatalf("error is %v", err)
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("the refusal happened after the file was created")
	}

	// Acknowledged, and it goes through — with the acknowledgement recorded.
	acknowledged, err := NewPlan(candidates, Policy{
		Acknowledgements: map[string]bool{"maps/e1m1.bsp": true, "gfx/palette.lmp": true},
	}, target)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	result, err := Create(acknowledged, Options{
		Output: out, Now: fixedTime,
		Review: ReviewRecord{
			Acknowledged: []string{"maps/e1m1.bsp", "gfx/palette.lmp"},
			Reason:       "my own map, compiled by hand before this machine had a build set up",
		},
	})
	if err != nil {
		t.Fatalf("an acknowledged plan was refused: %v", err)
	}
	if len(result.Manifest.Review.Acknowledged) != 2 {
		t.Fatalf("the review record is %+v", result.Manifest.Review)
	}
	for _, entry := range result.Manifest.Contents {
		if !entry.Acknowledged {
			t.Fatalf("%s was packaged without its acknowledgement recorded", entry.Path)
		}
	}
}

func TestCreateRecordsWhatWasLeftOut(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "mine/map.bsp", "level")
	writeFile(t, root, "elsewhere/thing.dat", "unknown")
	target := mustTarget(t, "quake-pak")

	candidates, err := Collect([]DirSource{{Dir: root}}, nil, target)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	// One file is declared as the author's, the other is not; the second is
	// held, and the plan is written without it only because it is dropped
	// rather than acknowledged.
	plan, err := NewPlan(candidates, Policy{AuthoredRoots: []string{filepath.Join(root, "mine")}}, target)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	if len(plan.NeedsReview()) != 1 {
		t.Fatalf("%d files need review, want 1", len(plan.NeedsReview()))
	}
	// Selecting only the declared tree is how a user resolves it.
	onlyMine, err := Collect([]DirSource{{Dir: filepath.Join(root, "mine")}}, nil, target)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	narrowed, err := NewPlan(onlyMine, Policy{AuthoredRoots: []string{filepath.Join(root, "mine")}}, target)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	result, err := Create(narrowed, Options{Output: filepath.Join(t.TempDir(), "x.pak"), Now: fixedTime})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(result.Manifest.Contents) != 1 {
		t.Fatalf("the package holds %d members, want 1", len(result.Manifest.Contents))
	}
}

func TestNoBuiltinTargetPutsAucomMetadataInsideTheArchive(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.txt", "a")
	for _, target := range Targets() {
		if target.AllowsMetadata {
			t.Fatalf("the built-in target %s permits metadata inside a game archive", target.ID)
		}
		t.Run(target.ID, func(t *testing.T) {
			plan := planFrom(t, root, target, Policy{})
			out := filepath.Join(t.TempDir(), "x"+target.Extension())
			_, err := Create(plan, Options{Output: out, EmbedManifest: true, Now: fixedTime})
			if err == nil {
				t.Fatal("metadata was embedded in a target that does not permit it")
			}
			if !strings.Contains(err.Error(), "does not permit") {
				t.Fatalf("error is %v", err)
			}
			// And the ordinary path writes nothing of its own into the archive.
			result, err := Create(plan, Options{Output: out, Now: fixedTime})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if result.Manifest.MetadataEmbedded {
				t.Fatal("the manifest claims it was embedded")
			}
			for _, entry := range result.Entries {
				if entry.Path != "a.txt" {
					t.Fatalf("the archive holds %q, which nobody selected", entry.Path)
				}
			}
		})
	}
}

func TestATargetThatPermitsMetadataGetsIt(t *testing.T) {
	// The mechanism exists and works; no built-in target turns it on.
	root := t.TempDir()
	writeFile(t, root, "a.txt", "a")
	target := mustTarget(t, "quake3-pk3")
	target.ID = "test-pk3-with-metadata"
	target.AllowsMetadata = true
	target.MetadataPath = "aucom/package.json"

	plan := planFrom(t, root, target, Policy{})
	out := filepath.Join(t.TempDir(), "x.pk3")
	result, err := Create(plan, Options{Output: out, EmbedManifest: true, Companion: "1.0", Now: fixedTime})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !result.Manifest.MetadataEmbedded || result.Manifest.MetadataPath != "aucom/package.json" {
		t.Fatalf("manifest says %v / %q", result.Manifest.MetadataEmbedded, result.Manifest.MetadataPath)
	}
	inspection, err := Inspect(out, FormatPK3, Budget{})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	found := false
	for _, entry := range inspection.Entries {
		if entry.Path == "aucom/package.json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the embedded manifest is not in the archive: %+v", inspection.Entries)
	}
}

func TestCollectRefusesSymlinksAndDevices(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "real.txt", "x")
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Skipf("this filesystem does not do symbolic links: %v", err)
	}
	_, err := Collect([]DirSource{{Dir: root}}, nil, mustTarget(t, "quake-pak"))
	if err == nil {
		t.Fatal("a symbolic link was collected")
	}
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("error is %v, want an ErrUnsafePath", err)
	}
	if !strings.Contains(err.Error(), "/etc/passwd") {
		t.Fatalf("error is %v, want it to say where the link points", err)
	}
}

func TestCollectRefusesTwoSourcesForOneMemberPath(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	writeFile(t, a, "maps/e1m1.bsp", "one")
	writeFile(t, b, "maps/e1m1.bsp", "two")
	_, err := Collect([]DirSource{{Dir: a}, {Dir: b}}, nil, mustTarget(t, "quake-pak"))
	if err == nil {
		t.Fatal("two files were collected onto one member path")
	}
	if !strings.Contains(err.Error(), "packaged twice") {
		t.Fatalf("error is %v", err)
	}
}

func TestCollectAppliesAPrefix(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "e1m1.bsp", "level")
	candidates, err := Collect([]DirSource{{Dir: root, Prefix: "maps"}}, nil, mustTarget(t, "quake-pak"))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Path != "maps/e1m1.bsp" {
		t.Fatalf("collected %+v", candidates)
	}
}

func TestPreviewShowsEveryPathSourceSizeAndDecision(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "maps/e1m1.bsp", "level")
	writeFile(t, root, "gfx/palette.lmp", "palette")
	target := mustTarget(t, "quake-pak")
	candidates, err := Collect([]DirSource{{Dir: root}}, nil, target)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	plan, err := NewPlan(candidates, Policy{AuthoredRoots: []string{root}}, target)
	if err != nil {
		t.Fatalf("NewPlan: %v", err)
	}
	preview := plan.Preview()
	for _, want := range []string{
		"maps/e1m1.bsp", "gfx/palette.lmp",
		filepath.Join(root, "maps"), // the source path
		"authored", "authored-root",
		"2 to package",
	} {
		if !strings.Contains(preview, want) {
			t.Fatalf("the preview does not mention %q:\n%s", want, preview)
		}
	}
	if !strings.Contains(preview, "reproducibility portable") {
		t.Fatalf("the preview does not state the reproducibility promise:\n%s", preview)
	}
}

// AUT/AUCOM 219: `package preview` over 2100 files reported "2100 to package,
// 0 awaiting review, 0 refused" and `package create` on the same selection
// refused with "2100 members, over quake-pak's 2048". The ceiling lived only
// inside the writers, so the one command whose entire job is to say what the
// writer will do was the one command that could not say it.
func TestAPlanRefusesTheCeilingAWriterWouldRefuse(t *testing.T) {
	target := mustTarget(t, "quake-pak")
	root := t.TempDir()
	for i := 0; i <= target.MaxEntries; i++ {
		writeFile(t, root, fmt.Sprintf("f%05d.txt", i), strconv.Itoa(i))
	}

	plan := planFrom(t, root, target, Policy{})
	blocked := plan.Blocked()
	if blocked == nil {
		t.Fatalf("a plan of %d members did not refuse %s's ceiling of %d",
			len(plan.Included()), target.ID, target.MaxEntries)
	}
	if !strings.Contains(blocked.Error(), "over quake-pak's") {
		t.Errorf("the refusal does not name the ceiling: %v", blocked)
	}

	// And the writer must still refuse it: one rule asked from two places, not
	// two rules that agree today.
	if _, err := Create(plan, Options{
		Output: filepath.Join(t.TempDir(), "over.pak"), Now: fixedTime,
	}); err == nil {
		t.Error("the writer accepted what the plan refused")
	}
}
