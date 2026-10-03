package web

import (
	"os/exec"
	"testing"
)

// The build result names the account revision it built and the digest of the
// `.map` the compiler was handed (Q3_012A). A map saved to the account three
// times read "revision 0" — its APMap's own counter — and the panel showed the
// APMap's digest and the conversion record's, never the converted file's.
func TestTheBuildResultNamesTheSavedRevisionAndTheCompiledMap(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed; the page's build-result lines were NOT exercised")
	}
	out, err := exec.Command("node", "testdata/buildread.check.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Logf("%s", out)
}
