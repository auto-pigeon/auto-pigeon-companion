package threat

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// repoRoot is the module root, two directories up from this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// The whole point of the package. Every row is structurally complete, every
// piece of evidence names a test that is in this repository, every category has
// something in it, and every accepted residual risk has somebody's name on it
// and a date that has not passed.
//
// When this fails on a DATE, it has not broken. A residual risk whose review
// date has gone by is one nobody has looked at since it was accepted, and being
// made to look at it again is what writing the date down was for.
func TestTheThreatMatrixHoldsUp(t *testing.T) {
	problems := Check(repoRoot(t), time.Now())
	for _, problem := range problems {
		t.Errorf("%s", problem)
	}
}

// A model that only describes what is already fixed is a model nobody learns
// from. This is the guard against the matrix quietly becoming that: at least
// one row has to carry an accepted residual risk, and the known ones — the
// token in config.json, the unsigned artifacts — have to still be there or have
// been genuinely fixed.
func TestTheMatrixStillSaysWhatIsNotSolved(t *testing.T) {
	withResidual := 0
	for _, row := range Matrix() {
		if row.Residual != nil {
			withResidual++
		}
	}
	if withResidual == 0 {
		t.Error("no row records a residual risk. Either everything is solved, which it is not, " +
			"or the register has been emptied instead of reviewed")
	}
}

func TestEveryCategoryHasARowAndEveryRowHasAKnownCategory(t *testing.T) {
	for _, category := range Categories {
		if len(InCategory(category)) == 0 {
			t.Errorf("category %q has no row", category)
		}
	}
	for _, row := range Matrix() {
		if !knownCategory(row.Category) {
			t.Errorf("%s is in undeclared category %q", row.ID, row.Category)
		}
	}
}

func TestFindReturnsARowAndReportsAMissingOne(t *testing.T) {
	row, ok := Find("T01")
	if !ok {
		t.Fatal("T01 is not in the matrix")
	}
	if row.Category != CatDocument {
		t.Errorf("T01 is in %q", row.Category)
	}
	if _, ok := Find("T999"); ok {
		t.Error("a row that is not there was found")
	}
}

// The checker has to actually catch things, or the test above is a test of
// nothing. Each of these is a way a threat model rots, driven through Check
// against a repository that has no test files at all.
func TestTheCheckerCatchesEachWayTheMatrixCanRot(t *testing.T) {
	empty := t.TempDir()
	problems := Check(empty, time.Now())
	if len(problems) == 0 {
		t.Fatal("checking the matrix against a repository with no tests reported nothing")
	}
	joined := ""
	for _, problem := range problems {
		joined += problem.String() + "\n"
	}
	if !strings.Contains(joined, "which has no test files in this repository") {
		t.Errorf("missing evidence was not reported:\n%s", joined)
	}
}

// Expired is what a person runs to see what is due. It has to agree with Check.
func TestExpiredAgreesWithTheChecker(t *testing.T) {
	far := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	overdue := Expired(far)
	if len(overdue) == 0 {
		t.Fatal("no residual risk is ever due for review")
	}
	problems := Check(repoRoot(t), far)
	found := 0
	for _, problem := range problems {
		if strings.Contains(problem.What, "has passed") {
			found++
		}
	}
	if found != len(overdue) {
		t.Errorf("Expired reports %d overdue and Check reports %d", len(overdue), found)
	}
}

// A row's mitigation has to say something about mechanism. This catches the
// failure where a matrix fills up with "handled by validation".
func TestNoRowHidesBehindAVagueMitigation(t *testing.T) {
	for _, row := range Matrix() {
		if len(row.Mitigation) < 60 {
			t.Errorf("%s's mitigation is %d characters: %q. Say what stops it and where that lives",
				row.ID, len(row.Mitigation), row.Mitigation)
		}
		if len(row.Vector) < 40 {
			t.Errorf("%s's vector is too short to be a scenario: %q", row.ID, row.Vector)
		}
	}
}

// A manual procedure that does not say what to do and what to look for is not a
// procedure.
func TestEveryManualProcedureNamesAnObservation(t *testing.T) {
	for _, row := range Matrix() {
		if row.Manual == "" {
			continue
		}
		if !strings.Contains(row.Manual, "Expected") {
			t.Errorf("%s's manual procedure does not say what to expect: %q", row.ID, row.Manual)
		}
		if !strings.Contains(row.Manual, "companion ") {
			t.Errorf("%s's manual procedure names no command to run", row.ID)
		}
	}
}
