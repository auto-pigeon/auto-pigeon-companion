package web

import "testing"

// The claim is what is owned, not the key: A's goroutine can be anywhere in its
// way out when B takes the request, and B may not even have an id yet.
func TestAReplacedBuildCannotFreeOrRenameWhatReplacedIt(t *testing.T) {
	runs := newBuildRuns()
	a, holder := runs.claim("K")
	if a == nil || holder != nil {
		t.Fatalf("the first claim: %v, %v", a, holder)
	}
	runs.named(a, "build-A")
	runs.release(a)

	b, _ := runs.claim("K")
	if b == nil {
		t.Fatal("the request was not free after A released it")
	}
	// A, late: its notification of its name, then the deferred release, with
	// B holding the key and no id yet.
	runs.named(a, "build-A")
	runs.release(a)

	mine, holder := runs.claim("K")
	if mine != nil || holder != b {
		t.Fatalf("C was given %v, holder %v; want B holding it", mine, holder)
	}
	if id := runs.idOf(holder); id != "" {
		t.Fatalf("B has no id yet, and C was told %q", id)
	}

	// And once it has one, the refusal is B's, whatever A does.
	runs.named(b, "build-B")
	runs.named(a, "build-A")
	runs.release(a)
	if _, holder = runs.claim("K"); holder != b || runs.idOf(holder) != "build-B" {
		t.Fatalf("C was refused with %v", holder)
	}

	// Release is the owner's, once. A second one is nothing.
	runs.release(b)
	runs.release(b)
	if mine, _ = runs.claim("K"); mine == nil {
		t.Fatal("the request was not free after B released it")
	}
	runs.release(b)
	if _, holder = runs.claim("K"); holder != mine {
		t.Fatalf("B's second release freed the claim that replaced it: %v", holder)
	}
}
