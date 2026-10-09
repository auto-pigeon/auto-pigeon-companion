package web

import (
	"io/fs"
	"strings"
	"testing"
)

// A finding's hint is what to do about it, and the page left it out: the
// terminal printed a rule's `hint` under its sentence, the Build page printed
// the sentence and the compiler's line and stopped (`Q3_012B` — seen headed, on
// a real Q3Map2 `LoadPCX: Couldn't read …` build, where the hint is the only
// place that says where the file has to be). The page is the only place this
// can be put right, so this reads what its findings list renders.
func TestBuildPageShowsAFindingsHint(t *testing.T) {
	source, err := fs.ReadFile(assetsFS(), "build.js")
	if err != nil {
		t.Fatalf("%v", err)
	}
	start := strings.Index(string(source), "function findingsList(step)")
	if start < 0 {
		t.Fatal("build.js has no findingsList(step): this test reads what a stage's findings render")
	}
	body := string(source)[start:]
	if end := strings.Index(body, "\n  }\n"); end > 0 {
		body = body[:end]
	}
	for _, want := range []string{"finding.message", "finding.class", "finding.raw", "finding.hint"} {
		if !strings.Contains(body, want) {
			t.Errorf("the findings list does not render %s", want)
		}
	}
}
