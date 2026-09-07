package pack

import (
	"strings"
	"testing"
)

func TestCheckEntryPathRefusals(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"empty", "", "is empty"},
		{"absolute", "/etc/passwd", "is absolute"},
		{"parent traversal", "../../etc/passwd", "escapes the archive"},
		{"embedded traversal", "maps/../../etc/passwd", "escapes the archive"},
		{"single dot element", "maps/./e1m1.bsp", "escapes the archive"},
		{"backslash", `maps\e1m1.bsp`, "contains a backslash"},
		{"windows drive", "C:/maps/e1m1.bsp", "names a Windows drive"},
		{"unc", "//server/share/x.bsp", "is absolute"},
		{"home", "~/maps/e1m1.bsp", "starts at a home directory"},
		{"nul", "maps/e1m1\x00.bsp", "contains a NUL byte"},
		{"control byte", "maps/e1m1\n.bsp", "and this build writes printable ASCII only"},
		{"non ascii", "maps/é.bsp", "printable ASCII only"},
		{"empty element", "maps//e1m1.bsp", "has an empty path element"},
		{"trailing slash", "maps/", "ends in a separator"},
		{"trailing dot", "maps/e1m1.", "Windows cannot store"},
		{"trailing space", "maps/e1m1 ", "Windows cannot store"},
		{"leading space", "maps/ e1m1.bsp", "Windows cannot store"},
		{"device", "maps/aux.bsp", "reserved Windows device"},
		{"device bare", "NUL", "reserved Windows device"},
		{"device com", "sound/com1.wav", "reserved Windows device"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckEntryPath(tc.path, PK3NameLength)
			if err == nil {
				t.Fatalf("CheckEntryPath(%q) allowed it", tc.path)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CheckEntryPath(%q) = %v, want it to mention %q", tc.path, err, tc.want)
			}
		})
	}
}

func TestCheckEntryPathAccepts(t *testing.T) {
	for _, path := range []string{
		"e1m1.bsp",
		"maps/e1m1.bsp",
		"gfx/env/sky_up.tga",
		"sound/ambience/water1.wav",
		"maps/my map.bsp", // a space inside an element is fine; only the edges are not
		"a/b/c/d/e/f/g.txt",
	} {
		if err := CheckEntryPath(path, PK3NameLength); err != nil {
			t.Fatalf("CheckEntryPath(%q) = %v, want nil", path, err)
		}
	}
}

func TestCheckEntryPathLength(t *testing.T) {
	// PAK's name field holds 55 bytes plus the terminator, and a path one byte
	// longer is the difference between an archive an engine reads and one it
	// reads off the end of.
	fits := "maps/" + strings.Repeat("a", PAKNameLength-len("maps/"))
	if err := CheckEntryPath(fits, PAKNameLength); err != nil {
		t.Fatalf("a %d-byte path was refused: %v", len(fits), err)
	}
	if err := CheckEntryPath(fits+"a", PAKNameLength); err == nil {
		t.Fatal("a 56-byte path was accepted into PAK's 56-byte field, leaving no terminator")
	}
}

func TestFindCollisions(t *testing.T) {
	collisions := FindCollisions([]string{
		"maps/e1m1.bsp",
		"maps/E1M1.bsp",
		"gfx/palette.lmp",
		"sound/x.wav",
		"sound/x.wav",
	})
	var kinds []string
	for _, c := range collisions {
		kinds = append(kinds, c.Kind)
	}
	if len(collisions) != 2 {
		t.Fatalf("got %d collisions (%v), want a duplicate and a case collision", len(collisions), kinds)
	}
	byKind := map[string]Collision{}
	for _, c := range collisions {
		byKind[c.Kind] = c
	}
	caseCollision, ok := byKind["case"]
	if !ok {
		t.Fatalf("no case collision reported, got %v", kinds)
	}
	if got := caseCollision.Error(); !strings.Contains(got, "capitalisation") {
		t.Fatalf("case collision message is %q", got)
	}
	duplicate, ok := byKind["duplicate"]
	if !ok {
		t.Fatalf("no duplicate reported, got %v", kinds)
	}
	if !strings.Contains(duplicate.Error(), "sound/x.wav") {
		t.Fatalf("duplicate message is %q", duplicate.Error())
	}
}

func TestFindCollisionsIsStable(t *testing.T) {
	paths := []string{"b/x", "A/x", "a/x", "B/x", "c"}
	first := FindCollisions(paths)
	for i := 0; i < 20; i++ {
		again := FindCollisions(paths)
		if len(again) != len(first) {
			t.Fatalf("run %d found %d collisions, first run found %d", i, len(again), len(first))
		}
		for j := range again {
			if again[j].Error() != first[j].Error() {
				t.Fatalf("run %d differs at %d: %q vs %q", i, j, again[j].Error(), first[j].Error())
			}
		}
	}
}

func TestNormalizeEntryPathConvertsSeparatorsOnly(t *testing.T) {
	got, err := NormalizeEntryPath(`maps\e1m1.bsp`, PK3NameLength)
	if err != nil {
		t.Fatalf("NormalizeEntryPath: %v", err)
	}
	if got != "maps/e1m1.bsp" {
		t.Fatalf("got %q, want maps/e1m1.bsp", got)
	}
	// It converts; it does not repair. A traversal stays a refusal.
	if _, err := NormalizeEntryPath(`..\..\etc\passwd`, PK3NameLength); err == nil {
		t.Fatal("a Windows-spelled traversal was normalized into an accepted path")
	}
}

func TestCaseKeyHasNoLocaleInIt(t *testing.T) {
	// The Turkish dotless-i problem cannot arise, because the byte that causes
	// it is not one CheckEntryPath accepts. Asserted so a future widening of
	// the character rules has to come past this test.
	if err := CheckEntryPath("maps/\u0130.bsp", PK3NameLength); err == nil {
		t.Fatal("a non-ASCII path was accepted; CaseKey's correctness depends on it not being")
	}
	if CaseKey("MAPS/E1M1.BSP") != "maps/e1m1.bsp" {
		t.Fatalf("CaseKey folded unexpectedly: %q", CaseKey("MAPS/E1M1.BSP"))
	}
}
