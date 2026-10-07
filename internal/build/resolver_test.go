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
	tool, err := builtin.Find(builtin.EricwQ1)
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
	if got := steps[0].Profile.Meta.ID; got != builtin.EricwQ1 {
		t.Errorf("the built-in leak test ran %s", got)
	}
	if entry, ok := resolver.EntryForStep(leak.Steps[0]); !ok || entry.Profile.Metadata().ID != builtin.EricwQ1 {
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
	if err == nil || !strings.Contains(err.Error(), builtin.EricwQ1) || !strings.Contains(err.Error(), "local.tool.my-ericw") {
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
