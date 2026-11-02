package build

import (
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

type listCatalog []job.CatalogEntry

func (c listCatalog) List() ([]job.CatalogEntry, error) { return c, nil }

// twoEricWs is the built-in EricW tool and a user's own copy of it under
// another id: two installed tools that both provide q1.bsp.compile — what the
// New profile wizard invites (NEW_310, found live on Windows).
func twoEricWs(t *testing.T) (listCatalog, *profile.PipelineProfile) {
	t.Helper()
	tool, err := builtin.Find(builtin.EricwAutoPigeonQ1)
	if err != nil {
		t.Fatal(err)
	}
	own := *tool.Profile.(*profile.ToolProfile)
	own.Meta.ID = "local.tool.my-ericw"
	leak, err := builtin.Find("auto-pigeon.q1.leak-test")
	if err != nil {
		t.Fatal(err)
	}
	return listCatalog{
		{Profile: tool.Profile, Trust: profile.TrustBuiltin},
		{Profile: &own, Trust: profile.TrustLocal},
	}, leak.Profile.(*profile.PipelineProfile)
}

// A stage that names its tool resolves to that tool and is not ambiguous; the
// same stage without a name is still refused, by both names.
func TestAStageThatNamesItsToolIsNeverAmbiguous(t *testing.T) {
	catalog, leak := twoEricWs(t)
	resolver, err := NewResolver(catalog)
	if err != nil {
		t.Fatal(err)
	}

	if err := resolver.CheckSteps(leak.Steps); err != nil {
		t.Fatalf("the built-in leak test names its tool, and was refused: %v", err)
	}
	steps, err := leak.Resolve(resolver)
	if err != nil {
		t.Fatal(err)
	}
	if got := steps[0].Profile.Meta.ID; got != builtin.EricwAutoPigeonQ1 {
		t.Errorf("the built-in leak test ran %s", got)
	}
	if entry, ok := resolver.EntryForStep(leak.Steps[0]); !ok || entry.Profile.Metadata().ID != builtin.EricwAutoPigeonQ1 {
		t.Errorf("the recorded tool is %v", entry.Profile)
	}

	// The user's own tool, named on the stage, is the one that runs.
	mine := *leak
	mine.Steps = append([]profile.PipelineStep(nil), leak.Steps...)
	mine.Steps[0].Tool = "local.tool.my-ericw"
	if steps, err := mine.Resolve(resolver); err != nil || steps[0].Profile.Meta.ID != "local.tool.my-ericw" {
		t.Errorf("a stage naming the user's tool: %v %v", steps, err)
	}

	// Unnamed: refused rather than ranked, naming both.
	unnamed := *leak
	unnamed.Steps = append([]profile.PipelineStep(nil), leak.Steps...)
	unnamed.Steps[0].Tool = ""
	err = resolver.CheckSteps(unnamed.Steps)
	if err == nil || !strings.Contains(err.Error(), builtin.EricwAutoPigeonQ1) || !strings.Contains(err.Error(), "local.tool.my-ericw") {
		t.Errorf("an unnamed stage with two providers: %v", err)
	}

	// A named tool that is not installed is a refusal that names it.
	missing := *leak
	missing.Steps = append([]profile.PipelineStep(nil), leak.Steps...)
	missing.Steps[0].Tool = "local.tool.gone"
	if _, err := missing.Resolve(resolver); err == nil || !strings.Contains(err.Error(), "local.tool.gone") {
		t.Errorf("a stage naming a tool that is not installed: %v", err)
	}
}

// NEW_380: the forks stand alone, so a stage a person left unnamed is answered by
// the one built-in tool of its game, with nothing to rank and no conflict.
func TestAnUnnamedStageIsAnsweredByTheMaintainedBuild(t *testing.T) {
	for _, id := range []string{builtin.EricwAutoPigeonQ1, builtin.EricwAutoPigeonQ2, builtin.Q3Map2AutoPigeon} {
		fork, err := builtin.Find(id)
		if err != nil {
			t.Fatal(err)
		}
		resolver, err := NewResolver(listCatalog{{Profile: fork.Profile, Trust: profile.TrustBuiltin}})
		if err != nil {
			t.Fatal(err)
		}
		capability := fork.Profile.(*profile.ToolProfile).Actions[0].Capability
		unnamed := []profile.PipelineStep{{ID: "compile", Capability: capability}}
		if err := resolver.CheckSteps(unnamed); err != nil || len(resolver.Conflicts) != 0 {
			t.Errorf("%s: an unnamed stage: %v, conflicts %v", id, err, resolver.Conflicts)
		}
		if tool, _, ok := resolver.ProviderForStep(unnamed[0]); !ok || tool.Meta.ID != id {
			t.Errorf("an unnamed stage resolved to %v, want %s", tool, id)
		}
	}
}

// NEW_380: a user's pipeline that names a retired built-in tool keeps naming it.
// It is not rewritten and does not fall back to the fork, and it is told, by
// name, what took that tool's place.
func TestAStageNamingARetiredToolIsToldWhatReplacedItAndDoesNotFallBack(t *testing.T) {
	fork, err := builtin.Find(builtin.EricwAutoPigeonQ1)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver(listCatalog{{Profile: fork.Profile, Trust: profile.TrustBuiltin}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := resolver.ProviderFrom("auto-pigeon.ericw-tools.q1", "q1.bsp.compile"); ok {
		t.Fatal("a retired tool resolved to something")
	}
	problem, fix := resolver.ExplainNamedTool("auto-pigeon.ericw-tools.q1", "q1.bsp.compile")
	if !strings.Contains(problem, "no longer ships") || !strings.Contains(fix, builtin.EricwAutoPigeonQ1) {
		t.Errorf("explained as %q / %q", problem, fix)
	}
	// Anything else that is missing is still only missing.
	if problem, _ := resolver.ExplainNamedTool("local.tool.gone", "q1.bsp.compile"); !strings.Contains(problem, "is not installed") {
		t.Errorf("a missing user tool: %q", problem)
	}
}

// Only a BUILT-IN alternative is exempt. A user's own tool that borrows the id is
// an ordinary second provider, refused as every other one is.
func TestAUserProfileCannotBorrowTheAlternativeExemption(t *testing.T) {
	upstream, err := builtin.Find(builtin.EricwAutoPigeonQ1)
	if err != nil {
		t.Fatal(err)
	}
	own := *upstream.Profile.(*profile.ToolProfile)
	own.Meta.ID = builtin.EricwAutoPigeonQ1
	resolver, err := NewResolver(listCatalog{
		{Profile: upstream.Profile, Trust: profile.TrustBuiltin},
		{Profile: &own, Trust: profile.TrustLocal},
	})
	if err != nil {
		t.Fatal(err)
	}
	capability := own.Actions[0].Capability
	if err := resolver.CheckSteps([]profile.PipelineStep{{ID: "compile", Capability: capability}}); err == nil {
		t.Error("a locally installed profile carrying the alternative's id did not conflict")
	}
}
