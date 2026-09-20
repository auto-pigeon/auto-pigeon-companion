package build

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// `AUCOM/AUE/AUT 246I1`. A texture collection is a DIRECTORY, and the half-fix
// that classified it as a folder in the browser while the request model still
// ran every value through a file check is what these tests pin shut.

// textureRoot writes a directory that looks like a verified bundle's content
// root: two WADs, in declaration order.
func (h *harness) textureRoot(names ...string) string {
	h.t.Helper()

	dir := filepath.Join(h.dir, "textures")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		h.t.Fatalf("%v", err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("WAD2"+name), 0o600); err != nil {
			h.t.Fatalf("%v", err)
		}
	}

	return dir
}

func bundleRequest(h *harness, root string) Request {
	return Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("dm1.map", "MAP")},
		Roots:      map[string]string{profile.RootContent: root},
		RootSources: map[string]RootSource{profile.RootContent: {
			Kind: RootFromTextureBundle,
			Bundle: &BundleRef{
				Schema: "aub-map-texture-export/1.1", Backend: "https://aub.example.test",
				MapID: "map0000000001", Revision: 7, Digest: strings.Repeat("a", 64),
				WADsDeclared: []string{"first.wad", "second.wad"},
				Files: []BundleFile{
					{Path: "first.wad", SHA256: strings.Repeat("b", 64), Bytes: 12},
					{Path: "second.wad", SHA256: strings.Repeat("c", 64), Bytes: 13},
				},
				CompilerReady: true,
			},
		}},
		Options: map[string]map[string]string{"compile": {"basename": "dm1"}},
	}
}

// The whole point of the change: the directory reaches the compiler, as one
// `-wadpath` argument followed by the root as a separate argv element — and the
// later stages, which do not declare the root, do not receive it.
func TestASuppliedContentRootReachesOnlyTheStepThatDeclaredIt(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	root := h.textureRoot("first.wad", "second.wad")

	manifest := h.mustRun(bundleRequest(h, root))

	compile := manifest.Steps[0]
	if compile.Command == nil {
		t.Fatalf("the compile step ran no command: %+v", compile)
	}
	at := slices.Index(compile.Command.Args, "-wadpath")
	if at < 0 {
		t.Fatalf("argv = %v, want -wadpath", compile.Command.Args)
	}
	if got := compile.Command.Args[at+1]; got != root {
		t.Errorf("-wadpath = %q, want the supplied root %q as one separate element", got, root)
	}
	for _, step := range manifest.Steps[1:] {
		if step.Command == nil {
			continue
		}
		if slices.Contains(step.Command.Args, "-wadpath") {
			t.Errorf("the %s step received -wadpath, and its action does not declare content_root: %v",
				step.ID, step.Command.Args)
		}
		for _, arg := range step.Command.Args {
			if arg == root {
				t.Errorf("the %s step received the content root: %v", step.ID, step.Command.Args)
			}
		}
	}
	// And the compiler really read it, which a resolved argv alone does not
	// prove: the fixture lists the directory it was handed.
	output := readJobLog(t, h, compile.JobID)
	if !strings.Contains(output, "wadpath holds first.wad") ||
		!strings.Contains(output, "wadpath holds second.wad") {
		t.Errorf("the compiler did not read the supplied root: %q", output)
	}
}

// The manifest records the bundle by identity, and the machine path only as
// diagnostic data.
func TestTheManifestRecordsWhichBundleSuppliedTheRoot(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	root := h.textureRoot("first.wad", "second.wad")

	manifest := h.mustRun(bundleRequest(h, root))

	if len(manifest.Roots) != 1 {
		t.Fatalf("roots = %+v, want one", manifest.Roots)
	}
	record := manifest.Roots[0]
	switch {
	case record.Role != profile.RootContent:
		t.Errorf("role = %q", record.Role)
	case record.Path != root:
		t.Errorf("path = %q, want %q", record.Path, root)
	case record.Access != profile.AccessRead:
		t.Errorf("access = %q, want the read the action declared", record.Access)
	case record.Source == nil || record.Source.Bundle == nil:
		t.Fatalf("source = %+v, want the bundle", record.Source)
	}
	bundle := record.Source.Bundle
	if bundle.MapID != "map0000000001" || bundle.Revision != 7 {
		t.Errorf("bundle identity = %s@%d", bundle.MapID, bundle.Revision)
	}
	if !slices.Equal(bundle.WADsDeclared, []string{"first.wad", "second.wad"}) {
		t.Errorf("declaration = %v, and the order is the map's own content", bundle.WADsDeclared)
	}
	if !bundle.CompilerReady {
		t.Error("the compiler-ready verdict was lost")
	}
}

// Two machines that built from the same verified bundle built the same thing.
// The directory it was cached in is not part of the recipe; the bundle's
// identity is.
func TestTheReproducibleKeyFollowsTheBundleAndNotThePath(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	first := h.mustRun(bundleRequest(h, h.textureRoot("first.wad", "second.wad")))

	// The same bundle, cached somewhere else.
	elsewhere := filepath.Join(h.dir, "another cache")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first.wad", "second.wad"} {
		if err := os.WriteFile(filepath.Join(elsewhere, name), []byte("WAD2"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	moved := h.mustRun(bundleRequest(h, elsewhere))
	if first.ReproducibleKey != moved.ReproducibleKey {
		t.Errorf("moving the cache changed the key:\n %s\n %s", first.ReproducibleKey, moved.ReproducibleKey)
	}

	// A DIFFERENT bundle is a different build, even at the same path.
	different := bundleRequest(h, elsewhere)
	different.RootSources[profile.RootContent].Bundle.Digest = strings.Repeat("d", 64)
	changed := h.mustRun(different)
	if changed.ReproducibleKey == first.ReproducibleKey {
		t.Error("a different bundle produced the same reproducible key")
	}

	// And a build with no texture root at all is a third thing.
	bare := h.mustRun(Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("dm1.map", "MAP")},
		Options:    map[string]map[string]string{"compile": {"basename": "dm1"}},
	})
	if bare.ReproducibleKey == first.ReproducibleKey {
		t.Error("a build with no texture root matched one with a bundle")
	}
}

// --- the refusals -----------------------------------------------------------

func TestABuildMayNotSupplyTheRootsItDoesNotOwn(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	root := h.textureRoot("first.wad")

	for _, role := range []string{profile.RootWorkspace, profile.RootBuild, profile.RootToolInstall} {
		request := Request{
			PipelineID: "test.build.pipeline",
			Inputs:     map[string]string{"source_map": h.sourceMap("dm1.map", "MAP")},
			Roots:      map[string]string{role: root},
		}
		if _, err := h.run(request); err == nil || !strings.Contains(err.Error(), role) {
			t.Errorf("supplying %q = %v, want a refusal naming it", role, err)
		}
	}
}

func TestARootNoStepDeclaresIsRefused(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	request := Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("dm1.map", "MAP")},
		Roots:      map[string]string{profile.RootGame: h.textureRoot("first.wad")},
	}
	if _, err := h.run(request); err == nil || !strings.Contains(err.Error(), profile.RootGame) {
		t.Fatalf("supplying an undeclared root = %v", err)
	}
}

func TestARootThatIsAFileIsRefusedAsAFile(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	file := h.sourceMap("one.wad", "WAD2")
	request := Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("dm1.map", "MAP")},
		Roots:      map[string]string{profile.RootContent: file},
	}
	_, err := h.run(request)
	if err == nil || !strings.Contains(err.Error(), "a root is a directory") {
		t.Fatalf("supplying a file as a root = %v, want the sentence that says which is which", err)
	}
}

func TestAnUnknownRootRoleIsRefused(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	request := Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("dm1.map", "MAP")},
		Roots:      map[string]string{"wad_folder": h.textureRoot("first.wad")},
	}
	if _, err := h.run(request); err == nil || !strings.Contains(err.Error(), "not a root role") {
		t.Fatalf("an invented role = %v", err)
	}
}

// A preview refuses what a run refuses. Finding out at stage three is what a
// preview exists to prevent.
func TestAPreviewRefusesTheSameRootsARunDoes(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	request := Request{
		PipelineID: "test.build.pipeline",
		Inputs:     map[string]string{"source_map": h.sourceMap("dm1.map", "MAP")},
		Roots:      map[string]string{profile.RootWorkspace: h.textureRoot("first.wad")},
	}
	if _, err := h.runner.Preview(request); err == nil {
		t.Fatal("a preview accepted a root a run refuses")
	}
}

// Preview and execution resolve to the same argv after path substitution. The
// runner checks it on every step; this is the check that the ROOT is part of
// what agrees.
func TestThePreviewShowsTheWadpathThatRuns(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	root := h.textureRoot("first.wad", "second.wad")
	request := bundleRequest(h, root)

	previewed, err := h.runner.Preview(request)
	if err != nil {
		t.Fatal(err)
	}
	at := slices.Index(previewed.Steps[0].Command.Args, "-wadpath")
	if at < 0 || previewed.Steps[0].Command.Args[at+1] != root {
		t.Fatalf("preview argv = %v, want -wadpath and the root", previewed.Steps[0].Command.Args)
	}
	manifest := h.mustRun(request)
	if !manifest.Steps[0].PreviewMatched {
		t.Errorf("the preview did not match what ran: %s", manifest.Steps[0].PreviewDifference)
	}
}

// A per-build root lives for one build. The binding the user granted is what
// configures the machine, and one map's textures do not get to rewrite it.
func TestASuppliedRootDoesNotTouchTheBinding(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	root := h.textureRoot("first.wad")

	before, _ := h.runner.binding("test.build.toolchain")
	h.mustRun(bundleRequest(h, root))
	after, _ := h.runner.binding("test.build.toolchain")

	if after.Roots[profile.RootContent] != before.Roots[profile.RootContent] {
		t.Errorf("the binding's content root changed from %q to %q",
			before.Roots[profile.RootContent], after.Roots[profile.RootContent])
	}
	if after.Roots[profile.RootContent] == root {
		t.Error("one build's texture directory was written into the persistent binding")
	}
}

// readJobLog reads a step's captured output from the job store.
func readJobLog(t *testing.T, h *harness, jobID string) string {
	t.Helper()

	if jobID == "" {
		t.Fatal("the step recorded no job")
	}
	body, err := h.service.Logs(jobID, "stdout", false)
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}
