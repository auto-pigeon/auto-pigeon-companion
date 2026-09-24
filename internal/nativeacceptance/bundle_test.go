package nativeacceptance_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/nativeacceptance"
)

func clockAt(text string) func() time.Time {
	when, err := time.Parse(time.RFC3339, text)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return when }
}

// The verdict is COMPUTED. A caller cannot write pass over a lane that failed,
// which is the property that makes a bundle worth merging at all.
func TestVerdictIsComputedNotDeclared(t *testing.T) {
	bundle := nativeacceptance.Bundle{
		Verdict: nativeacceptance.Pass,
		Lanes: []nativeacceptance.Lane{{
			ID: "artifact", State: nativeacceptance.Fail,
			Observations: []nativeacceptance.Observation{
				{Claim: "it works", State: nativeacceptance.Fail},
			},
		}},
	}
	bundle.Finish(clockAt("2026-09-08T10:00:00Z")())
	if bundle.Verdict != nativeacceptance.Fail {
		t.Fatalf("verdict %q survived a failing lane", bundle.Verdict)
	}
	if bundle.Counts.Fail != 1 {
		t.Fatalf("counts.fail = %d", bundle.Counts.Fail)
	}
}

// Not-available and not-applicable are not passes, and a bundle made entirely
// of them is not a pass with a caveat — it is a bundle whose verdict is pass
// and whose counts say nothing was checked, which is what a reader must see.
func TestUnavailableIsNotCountedAsAPass(t *testing.T) {
	bundle := nativeacceptance.Bundle{
		Lanes: []nativeacceptance.Lane{{
			ID: "compile", State: nativeacceptance.NotAvailable,
			Observations: []nativeacceptance.Observation{
				{Claim: "a compiler is bound", State: nativeacceptance.NotAvailable},
				{Claim: "this platform has a registry", State: nativeacceptance.NotApplicable},
			},
		}},
	}
	bundle.Finish(clockAt("2026-09-08T10:00:00Z")())
	if bundle.Counts.Pass != 0 {
		t.Fatalf("counts.pass = %d over two unrun observations", bundle.Counts.Pass)
	}
	if bundle.Counts.NotAvailable != 1 || bundle.Counts.NotApplicable != 1 {
		t.Fatalf("counts = %+v", bundle.Counts)
	}
}

// The redactor's boundary rule. `linux/amd64` is not an absolute path, and the
// first run of this kit refused to publish its own platform identity because
// the pattern said it was.
func TestRedactorLeavesATargetAlone(t *testing.T) {
	redactor := nativeacceptance.NewRedactor()
	got := redactor.Text("linux/amd64, go1.23.4, 8 cpu, little-endian")
	if got != "linux/amd64, go1.23.4, 8 cpu, little-endian" {
		t.Fatalf("a target was redacted: %q", got)
	}
}

func TestRedactorReplacesRootsAndKeepsTheTail(t *testing.T) {
	redactor := nativeacceptance.NewRedactor()
	redactor.Root("/home/somebody/games/Quake", "game_root")
	redactor.Root("/home/somebody", "home")
	got := redactor.Text("reading /home/somebody/games/Quake/id1/pak0.pak")
	if strings.Contains(got, "somebody") {
		t.Fatalf("the home directory survived: %q", got)
	}
	if !strings.Contains(got, "<game_root>/id1/pak0.pak") {
		t.Fatalf("the longest root did not win, or the tail was eaten: %q", got)
	}
}

func TestRedactorReplacesAnUnknownAbsolutePath(t *testing.T) {
	redactor := nativeacceptance.NewRedactor()
	for _, text := range []string{
		"error: /var/lib/something is not there",
		`error: C:\Users\somebody\thing is not there`,
		`error: \\fileserver\share\thing is not there`,
	} {
		got := redactor.Text(text)
		if !strings.Contains(got, "<path>") {
			t.Fatalf("%q was published as %q", text, got)
		}
		for _, secret := range []string{"somebody", "fileserver", "/var/lib"} {
			if strings.Contains(got, secret) {
				t.Fatalf("%q survived in %q", secret, got)
			}
		}
	}
}

// Validation is what a consumer runs, and it must refuse a document that a
// careless lane filled with a path.
func TestValidateRefusesALeakedPath(t *testing.T) {
	bundle := validBundle()
	bundle.Lanes[0].Observations[0].Detail = "opened /home/somebody/.config/thing"
	problems := bundle.Validate()
	if len(problems) == 0 {
		t.Fatal("a document holding an absolute path validated")
	}
	joined := strings.Join(problems, "; ")
	if !strings.Contains(joined, "absolute path") {
		t.Fatalf("the refusal does not say why: %s", joined)
	}
	// And it names WHERE without quoting WHAT.
	if strings.Contains(joined, "somebody") {
		t.Fatalf("the refusal quoted the thing it refused: %s", joined)
	}
}

func TestValidateRefusesAnInventedState(t *testing.T) {
	bundle := validBundle()
	bundle.Lanes[0].Observations[0].State = "probably"
	if problems := bundle.Validate(); len(problems) == 0 {
		t.Fatal("a state outside the closed set validated")
	}
}

func TestValidateRefusesAVerdictThatDisagreesWithTheLanes(t *testing.T) {
	bundle := validBundle()
	bundle.Lanes[0].State = nativeacceptance.Fail
	bundle.Verdict = nativeacceptance.Pass
	problems := strings.Join(bundle.Validate(), "; ")
	if !strings.Contains(problems, "not fail") {
		t.Fatalf("a pass over a failing lane validated: %s", problems)
	}
}

func TestValidateRefusesAGameRowThatIsNeitherPreviewNorLaunch(t *testing.T) {
	bundle := validBundle()
	bundle.Game = []nativeacceptance.GameRow{{
		Row: "played", Family: "quake1", State: nativeacceptance.Pass, Signal: "ready",
	}}
	if problems := bundle.Validate(); len(problems) == 0 {
		t.Fatal("a third kind of game row validated")
	}
}

// A game row has nowhere to put a byte of game data. This is asserted on the
// ENCODED document rather than on the struct, because what leaks is what is
// written.
func TestAGameRowCarriesOnlyItsAllowedFacts(t *testing.T) {
	bundle := validBundle()
	bundle.Game = []nativeacceptance.GameRow{{
		Row: "launch", Family: "quake1", ProfileID: "auto-pigeon.engine.q1-generic",
		ProfileVersion: "1.0.0", Platform: "linux/amd64",
		CommandShape: []string{"<engine>", "-basedir", "<game_root>", "+map", "start"},
		State:        nativeacceptance.Pass, Signal: "ready:still_running_at_deadline",
		ElapsedMS: 25000,
	}}
	bundle.Finish(clockAt("2026-09-08T10:00:00Z")())
	document, err := bundle.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(document, &decoded); err != nil {
		t.Fatal(err)
	}
	rows, _ := decoded["game"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected one game row, got %d", len(rows))
	}
	row, _ := rows[0].(map[string]any)
	allowed := map[string]bool{
		"row": true, "family": true, "profile_id": true, "profile_version": true,
		"platform": true, "command_shape": true, "state": true, "signal": true,
		"elapsed_ms": true, "detail": true,
	}
	for key := range row {
		if !allowed[key] {
			t.Fatalf("a game row carries %q, which nobody approved", key)
		}
	}
}

// The digest is over the document, so a byte changed in transit is detectable.
func TestDigestChangesWithTheDocument(t *testing.T) {
	first := nativeacceptance.Digest([]byte(`{"a":1}`))
	second := nativeacceptance.Digest([]byte(`{"a":2}`))
	if first == second {
		t.Fatal("two different documents have one digest")
	}
	if len(first) != 64 {
		t.Fatalf("digest is %d characters", len(first))
	}
}

// Every lane the runner performs has a title, so a listing cannot show a blank
// line for a lane somebody added without saying what it claims.
func TestEveryLaneHasATitle(t *testing.T) {
	for _, id := range nativeacceptance.LaneIDs {
		if strings.TrimSpace(nativeacceptance.LaneTitle(id)) == "" {
			t.Fatalf("lane %q has no title", id)
		}
	}
	if nativeacceptance.LaneIDs[len(nativeacceptance.LaneIDs)-1] != "purge" {
		t.Fatal("purge is no longer last, and it deletes what every lane above it produced")
	}
}

func TestCheckLanesNamesWhatItDoesNotKnow(t *testing.T) {
	unknown := nativeacceptance.CheckLanes([]string{"artifact", "compile", "sausages"})
	if len(unknown) != 1 || unknown[0] != "sausages" {
		t.Fatalf("CheckLanes = %v", unknown)
	}
}

// The fixture is what an operator's machine compiles, so its shape is asserted
// here rather than only observed by a compiler that may not be installed.
func TestFixtureIsASealedRoomAndOneSyntheticTexture(t *testing.T) {
	text := nativeacceptance.FixtureMap()
	if strings.Count(text, "\n{\n") < 4 {
		t.Fatal("the fixture has fewer entities than a worldspawn, two starts and a light")
	}
	for _, required := range []string{
		"\"classname\" \"worldspawn\"", "\"classname\" \"info_player_start\"",
		"\"classname\" \"info_player_deathmatch\"", "\"classname\" \"light\"",
		nativeacceptance.FixtureTexture, nativeacceptance.FixtureWADName,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("the fixture map does not contain %q", required)
		}
	}
	// Six faces per brush, six brushes.
	if got := strings.Count(text, nativeacceptance.FixtureTexture+" 0 0 0 1 1"); got != 36 {
		t.Fatalf("the sealed room has %d faces, not 36", got)
	}
	wad, err := nativeacceptance.FixtureWAD()
	if err != nil {
		t.Fatal(err)
	}
	if string(wad[:4]) != "WAD2" {
		t.Fatalf("the wad does not start with WAD2: %q", wad[:4])
	}
	// One miptex: 16x16 plus three mip levels, a 40-byte lump header (a
	// 16-byte name, two uint32 dimensions and four uint32 offsets) and a
	// 32-byte directory entry after the 12-byte file header.
	expected := 12 + (40 + 256 + 64 + 16 + 4) + 32
	if len(wad) != expected {
		t.Fatalf("the wad is %d bytes, not the %d the published layout gives", len(wad), expected)
	}
}

func validBundle() nativeacceptance.Bundle {
	bundle := nativeacceptance.Bundle{
		CompanionVersion: "0.0.0-test",
		Lanes: []nativeacceptance.Lane{{
			ID: "artifact", Title: "t", State: nativeacceptance.Pass,
			Observations: []nativeacceptance.Observation{
				{Claim: "it works", State: nativeacceptance.Pass, Detail: "42 bytes"},
			},
		}},
	}
	bundle.Finish(clockAt("2026-09-08T10:00:00Z")())
	return bundle
}
