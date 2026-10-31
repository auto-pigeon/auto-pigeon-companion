package web

import (
	"os/exec"
	"sync"
	"testing"
)

// NEW_365: a file build is admitted once per request, whichever tab asked.
func TestAFileBuildRequestIsAdmittedOnceUntilItEnds(t *testing.T) {
	runs := newBuildRuns()
	one := buildRequestKey(buildRequestBody{Pipeline: "local.pipeline.q1", Label: "first press"})
	same := buildRequestKey(buildRequestBody{Pipeline: "local.pipeline.q1", Label: "another tab"})
	other := buildRequestKey(buildRequestBody{Pipeline: "local.pipeline.q1", Strict: true})
	if one != same || one == other {
		t.Fatalf("keys: label must not matter and options must: %q %q %q", one, same, other)
	}
	var group sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	var owner *buildClaim
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if mine, _ := runs.claim(one); mine != nil {
				mu.Lock()
				admitted++
				owner = mine
				mu.Unlock()
			}
		}()
	}
	group.Wait()
	if admitted != 1 {
		t.Fatalf("%d of 16 concurrent presses admitted, want 1", admitted)
	}
	runs.named(owner, "20261028T160000Z-abcdef12")
	if mine, held := runs.claim(same); mine != nil || runs.idOf(held) != "20261028T160000Z-abcdef12" {
		t.Fatalf("a second tab = %v %v, want refused naming the running build", mine, held)
	}
	if mine, _ := runs.claim(other); mine == nil {
		t.Fatalf("a different request was refused")
	}
	runs.release(owner)
	if mine, _ := runs.claim(same); mine == nil {
		t.Fatalf("the same build after the first ended was refused")
	}
}

// NEW_365: the shared live line and the file form's check, run by node on the
// shipped scripts (testdata/liveline.check.mjs, testdata/buildreview.check.mjs).
func TestTheLiveLineAndTheFileFormsCheckBehaveAsShipped(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed; the live line and the file form's check were NOT exercised")
	}
	for _, check := range []string{"testdata/liveline.check.mjs", "testdata/buildreview.check.mjs"} {
		out, err := exec.Command("node", check).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", check, err, out)
		}
		t.Logf("%s", out)
	}
}
