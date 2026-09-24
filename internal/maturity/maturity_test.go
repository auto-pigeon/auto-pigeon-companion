package maturity_test

import (
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/maturity"
)

// The sentences `AUP/AUCOM 215` and `AUP/AUCOM 216` require, quoted from the
// prompts, byte for byte.
//
// They are written out here rather than compared against the constants they are
// checking, because a test that says `Quake2Message == Quake2Message` checks
// nothing. AUP's `frontend/src/services/gameMaturity.ts` carries the same two
// literals and the same two digests; neither repository can read the other at
// test time, which is why both pin the hashes.
const (
	requiredQ2 = "Quake II — Work in progress. Core editing is available, " +
		"but some textures, entities, compilation and engine workflows may be incomplete."
	requiredQ3 = "Quake III — Work in progress. Core editing is available, " +
		"but shader, patch, entity, compilation and engine workflows may be incomplete."
)

func TestTheSentencesAreTheOnesThatWereAskedFor(t *testing.T) {
	for _, c := range []struct {
		family, message, want, digest string
	}{
		{"quake2", maturity.Quake2Message, requiredQ2, maturity.Quake2MessageDigest},
		{"quake3", maturity.Quake3Message, requiredQ3, maturity.Quake3MessageDigest},
	} {
		if c.message != c.want {
			t.Errorf("the %s message has drifted:\n got: %q\nwant: %q", c.family, c.message, c.want)
		}
		if got := maturity.DigestOf(c.message); got != c.digest {
			t.Errorf("the pinned %s digest is %s and the message hashes to %s — AUP pins the same "+
				"value, so changing the wording is a change in two repositories or in neither",
				c.family, c.digest, got)
		}
	}
}

// The two sentences are not one sentence with the numeral swapped. Quake III's
// names shader and patch workflows because a Quake III map has a shader script
// and patches; Quake II's does not, because a Quake II map has neither. A
// change that derived one from the other would pass every other test here.
func TestTheTwoSentencesSayDifferentThings(t *testing.T) {
	if maturity.Quake2Message == maturity.Quake3Message {
		t.Fatal("the two families share a sentence")
	}
	for _, word := range []string{"shader", "patch"} {
		if !strings.Contains(maturity.Quake3Message, word) {
			t.Errorf("the Quake III sentence does not mention %s", word)
		}
		if strings.Contains(maturity.Quake2Message, word) {
			t.Errorf("the Quake II sentence mentions %s, which a Quake II map does not have", word)
		}
	}
	if maturity.Quake2MessageDigest == maturity.Quake3MessageDigest {
		t.Error("the two sentences pin the same digest")
	}
}

func TestQuake2AndQuake3AreWorkInProgressAndQuake1IsNot(t *testing.T) {
	for _, c := range []struct{ family, message string }{
		{"quake2", maturity.Quake2Message},
		{"quake3", maturity.Quake3Message},
	} {
		s := maturity.Of(c.family)
		if !s.IsWorkInProgress() {
			t.Errorf("%s is %q", c.family, s.State)
		}
		if s.Message != c.message {
			t.Errorf("the %s statement does not carry its own sentence", c.family)
		}
		if s.Badge == "" {
			t.Errorf("the %s statement has no badge, so a table has nothing to show", c.family)
		}
		if !s.FeedbackInvited {
			t.Errorf("%s does not invite a compatibility report, so the badge has no action beside it",
				c.family)
		}
	}

	q1 := maturity.Of("quake1")
	if q1.State != maturity.Stable {
		t.Errorf("quake1 is %q; the qualified Quake 1 path is not work in progress", q1.State)
	}
	if q1.Badge != "" || q1.Message != "" {
		t.Error("quake1 carries a badge; a badge on everything is a badge nobody reads")
	}
	if q1.FeedbackInvited {
		t.Error("quake1 invites a compatibility report; that action belongs to the unfinished path")
	}
}

// A family this build says nothing about is `undeclared`, and that is not a
// synonym for stable. Quake III was the live example until `216` shipped a
// Quake III toolchain; a family with no profiles behind it still is.
func TestAnUnknownFamilyIsUndeclaredAndNotStable(t *testing.T) {
	for _, family := range []string{"", "doom", "QUAKE4"} {
		s := maturity.Of(family)
		if s.State != maturity.Undeclared {
			t.Errorf("%q is %q", family, s.State)
		}
		if s.Badge != "" || s.Message != "" || s.FeedbackInvited {
			t.Errorf("%q carries a statement this build cannot make", family)
		}
	}
}

// Case and surrounding whitespace come from wherever the family was read, and
// none of the readers normalize it. The lookup does.
func TestTheLookupNormalizesWhatItIsGiven(t *testing.T) {
	for _, spelling := range []string{"quake3", "Quake3", " QUAKE3 ", "\tquake3\n"} {
		if !maturity.IsWorkInProgress(spelling) {
			t.Errorf("%q did not resolve to the Quake III statement", spelling)
		}
	}
}

// The statement is this build's, and there is no way for a document to supply
// one. The test is structural: the only input is a family string.
func TestOnlyTheFamilyDecides(t *testing.T) {
	families := maturity.Families()
	want := []string{"quake1", "quake2", "quake3"}
	if len(families) != len(want) {
		t.Fatalf("this build declares %v; it declares %v", families, want)
	}
	for i, name := range want {
		if families[i] != name {
			t.Fatalf("this build declares %v; it declares %v", families, want)
		}
	}
	// The sentences name the game in words a person uses, not the slug.
	for _, message := range []string{maturity.Quake2Message, maturity.Quake3Message} {
		if strings.Contains(message, "quake2") || strings.Contains(message, "quake3") {
			t.Errorf("the message shows the slug rather than the game's name: %q", message)
		}
	}
}
