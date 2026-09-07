package maturity_test

import (
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/maturity"
)

// The sentence `AUP/AUCOM 215` requires, quoted from the prompt, byte for byte.
//
// It is written out here rather than compared against the constant it is
// checking, because a test that says `Quake2Message == Quake2Message` checks
// nothing. AUP's `frontend/src/services/gameMaturity.ts` carries the same
// literal and the same digest; neither repository can read the other at test
// time, which is why both pin the hash.
const required = "Quake II — Work in progress. Core editing is available, " +
	"but some textures, entities, compilation and engine workflows may be incomplete."

func TestTheSentenceIsTheOneThatWasAskedFor(t *testing.T) {
	if maturity.Quake2Message != required {
		t.Errorf("the message has drifted:\n got: %q\nwant: %q", maturity.Quake2Message, required)
	}
	if got := maturity.DigestOf(maturity.Quake2Message); got != maturity.MessageDigest {
		t.Errorf("the pinned digest is %s and the message hashes to %s — AUP pins the same value, "+
			"so changing the wording is a change in two repositories or in neither",
			maturity.MessageDigest, got)
	}
}

func TestQuake2IsWorkInProgressAndQuake1IsNot(t *testing.T) {
	q2 := maturity.Of("quake2")
	if !q2.IsWorkInProgress() {
		t.Errorf("quake2 is %q", q2.State)
	}
	if q2.Message != maturity.Quake2Message {
		t.Error("the quake2 statement does not carry the sentence")
	}
	if q2.Badge == "" {
		t.Error("the quake2 statement has no badge, so a table has nothing to show")
	}
	if !q2.FeedbackInvited {
		t.Error("quake2 does not invite a compatibility report, so the badge has no action beside it")
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
// synonym for stable. Quake III is the live example: AUB's vocabulary has it,
// this build ships no profile for it, and claiming either "finished" or
// "unfinished" would be a claim about nothing.
func TestAnUnknownFamilyIsUndeclaredAndNotStable(t *testing.T) {
	for _, family := range []string{"", "quake3", "doom", "QUAKE4"} {
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
	for _, spelling := range []string{"quake2", "Quake2", " QUAKE2 ", "\tquake2\n"} {
		if !maturity.IsWorkInProgress(spelling) {
			t.Errorf("%q did not resolve to the Quake II statement", spelling)
		}
	}
}

// The statement is this build's, and there is no way for a document to supply
// one. The test is structural: the only input is a family string.
func TestOnlyTheFamilyDecides(t *testing.T) {
	families := maturity.Families()
	if len(families) != 2 || families[0] != "quake1" || families[1] != "quake2" {
		t.Errorf("this build declares %v; it declares quake1 and quake2", families)
	}
	// The sentence names the game in words a person uses, not the slug.
	if strings.Contains(maturity.Quake2Message, "quake2") {
		t.Error("the message shows the slug rather than the game's name")
	}
}
