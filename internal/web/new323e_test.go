package web

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// NEW_323E: Build & Run keeps the pipeline the user chose.
//
// A chosen pipeline that stopped being ready (its compiler rebound to a
// program its profile was not written for) was emptied out of the list, and
// the next refresh — or a reload — then selected the first READY pipeline: a
// different pipeline stood where the user's choice had been. The shipped
// chooser is run as it is by testdata/pipelinechoice.check.mjs.
func TestAPipelineThatLostItsReadinessStaysTheSelection(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable; the pipeline-choice table was not run")
	}
	out, err := exec.Command("node", "testdata/pipelinechoice.check.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Logf("%s", out)
}

// The page half the node table cannot see: Build asks the chooser to retain,
// remembers what was chosen where a reload can find it, and never starts or
// previews a selected pipeline that is not ready.
func TestBuildRemembersTheChosenPipelineAndStartsNothingUnready(t *testing.T) {
	source, err := os.ReadFile("assets/build.js")
	if err != nil {
		t.Fatal(err)
	}
	page := string(source)
	for _, want := range []string{
		`"Choose a ready pipeline…", true, true, undefined, { setupLabel: "pipelines" });`,
		`const previous = select.value || chosenPipeline || window.AUCOM.areas.play?.pipeline?.() || "";`,
		`rememberPipeline($("build-pipeline").value);`,
		// One button since NEW_363, one press since NEW_365: it says Build and
		// is disabled while the chosen pipeline is not ready, the reason said
		// beside it.
		`if (!window.AUCOM.pipelineReady(pipeline)) {`,
		`button.disabled = Boolean(blocked) || starting;`,
		`Nothing else was chosen for you.`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("assets/build.js no longer contains %q", want)
		}
	}
}
