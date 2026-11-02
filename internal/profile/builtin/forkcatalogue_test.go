package builtin

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin/retiredfixture"
)

// NEW_380: the compilers this program offers are Auto-Pigeon's own builds, and
// every bundled route resolves to one. Read from the documents themselves, not
// from a list of file names: a pipeline that keeps naming a deleted tool is
// exactly what deleting three JSON files would leave behind.

// retiredDocument is the upstream document a fork took over from, as it was
// shipped.
func retiredDocument(t *testing.T, fork string) *profile.ToolProfile {
	t.Helper()
	id := map[string]string{EricwAutoPigeonQ1: retiredfixture.EricwQ1, EricwAutoPigeonQ2: retiredfixture.EricwQ2,
		Q3Map2AutoPigeon: retiredfixture.Q3Map2}[fork]
	tool, err := retiredfixture.Tool(id)
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

func toolDocuments(t *testing.T) map[string]*profile.ToolProfile {
	t.Helper()
	entries, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*profile.ToolProfile{}
	for _, entry := range entries {
		if tool, ok := entry.Profile.(*profile.ToolProfile); ok {
			tools[tool.Meta.ID] = tool
		}
	}
	return tools
}

func TestTheOnlyBundledCompilersAreAutoPigeonsBuilds(t *testing.T) {
	var ids []string
	for id := range toolDocuments(t) {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	want := []string{EricwAutoPigeonQ1, EricwAutoPigeonQ2, Q3Map2AutoPigeon}
	sort.Strings(want)
	if strings.Join(ids, " ") != strings.Join(want, " ") {
		t.Fatalf("bundled tool profiles = %v, want exactly %v", ids, want)
	}
}

func TestEveryBundledPipelineStepResolvesToItsGamesFork(t *testing.T) {
	tools := toolDocuments(t)
	forFamily := map[string]string{"quake1": EricwAutoPigeonQ1, "quake2": EricwAutoPigeonQ2, "quake3": Q3Map2AutoPigeon}
	entries, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, entry := range entries {
		pipeline, ok := entry.Profile.(*profile.PipelineProfile)
		if !ok {
			continue
		}
		seen++
		want := forFamily[pipeline.GameProfile.EngineFamily]
		if want == "" {
			t.Fatalf("%s: no compiler for the family %q", pipeline.Meta.ID, pipeline.GameProfile.EngineFamily)
		}
		for _, step := range pipeline.Steps {
			if step.Tool != want {
				t.Errorf("%s step %s names %q, want %q", pipeline.Meta.ID, step.ID, step.Tool, want)
			}
			tool := tools[step.Tool]
			if tool == nil {
				t.Errorf("%s step %s names %q, which this build does not ship", pipeline.Meta.ID, step.ID, step.Tool)
				continue
			}
			provided := false
			for _, action := range tool.Actions {
				if action.Capability != step.Capability {
					continue
				}
				provided = true
				// An option the replacement does not declare would be silently
				// lost, or refused only when somebody builds.
				for name := range step.Options {
					declared := false
					for _, option := range action.Options {
						declared = declared || option.Name == name
					}
					if !declared {
						t.Errorf("%s step %s sets %q, which %s's %s does not declare", pipeline.Meta.ID, step.ID, name, tool.Meta.ID, action.ID)
					}
				}
			}
			if !provided {
				t.Errorf("%s step %s needs %q, which %s does not provide", pipeline.Meta.ID, step.ID, step.Capability, tool.Meta.ID)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no bundled pipelines at all")
	}
}

func TestOneBundledLeakTestPerGame(t *testing.T) {
	families := map[string][]string{}
	entries, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		pipeline, ok := entry.Profile.(*profile.PipelineProfile)
		if !ok || len(pipeline.Steps) != 1 {
			continue
		}
		families[pipeline.GameProfile.EngineFamily] = append(families[pipeline.GameProfile.EngineFamily], pipeline.Meta.ID)
	}
	for family, ids := range families {
		if len(ids) != 1 {
			t.Errorf("%s offers %d one-stage leak tests: %v", family, len(ids), ids)
		}
	}
	if len(DiagnosticPipelines) != 3 {
		t.Errorf("DiagnosticPipelines = %v, want one per game", DiagnosticPipelines)
	}
}

func TestARetiredProfileNamesItsMaintainedReplacement(t *testing.T) {
	cases := map[string]string{
		"auto-pigeon.ericw-tools.q1":                 EricwAutoPigeonQ1,
		"auto-pigeon.ericw-tools.q2":                 EricwAutoPigeonQ2,
		"auto-pigeon.q3map2":                         Q3Map2AutoPigeon,
		"auto-pigeon.q3.leak-test.auto-pigeon-build": Q3LeakTest,
	}
	for retired, want := range cases {
		got, ok := ReplacementFor(retired)
		if !ok || got != want {
			t.Errorf("ReplacementFor(%s) = %q, %v; want %q", retired, got, ok, want)
		}
		if _, err := Find(retired); err == nil {
			t.Errorf("%s is retired and still in the catalogue", retired)
		}
		if _, err := Find(got); err != nil {
			t.Errorf("the replacement of %s is not in the catalogue: %v", retired, err)
		}
	}
	if _, ok := ReplacementFor(Q1Normal); ok {
		t.Errorf("a current profile was called retired")
	}
}

// The retired documents are evidence for tests. A program that could reach them
// could offer them, so nothing but a test imports the package that holds them.
func TestNothingButATestReadsTheRetiredDocuments(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	const fixture = "internal/profile/builtin/retiredfixture"
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "dist" || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range parsed.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasSuffix(imported, fixture) {
				t.Errorf("%s imports the retired documents", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
