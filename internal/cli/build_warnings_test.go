package cli

import (
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// A compiler that exited 0 while it warned did not produce a finished map
// (`Q3_006`): Q3Map2 prints `Couldn't find image for shader` and writes the BSP
// anyway. The outcome line — the one a person reads first — must say so, and a
// build without warnings must still read as a plain success.
func TestASucceededBuildWithWarningsIsNotReportedAsComplete(t *testing.T) {
	env, stdout, _ := testEnv(t)
	warned := &build.Manifest{BuildID: "b_warned", State: job.Succeeded, Steps: []build.Step{{
		ID: "bsp", State: job.Succeeded,
		Diagnostics: []job.Diagnostic{
			{Severity: profile.SeverityWarning, Message: "A shader the map names has no image Q3Map2 could find."},
			{Severity: profile.SeverityInfo, Message: "A shader script was read out of the bound content."},
		},
	}}}
	if warned.Warnings() != 1 {
		t.Fatalf("Warnings() = %d, want 1 (info findings are not warnings)", warned.Warnings())
	}
	printBuildOutcome(env, warned)
	if !strings.Contains(stdout.String(), "build b_warned — succeeded with 1 warning(s): not a complete result") {
		t.Errorf("a warned build reads as complete:\n%s", stdout)
	}

	env, stdout, _ = testEnv(t)
	printBuildOutcome(env, &build.Manifest{BuildID: "b_clean", State: job.Succeeded})
	if !strings.Contains(stdout.String(), "build b_clean — succeeded\n") || strings.Contains(stdout.String(), "not a complete result") {
		t.Errorf("a clean build does not read as a plain success:\n%s", stdout)
	}
}
