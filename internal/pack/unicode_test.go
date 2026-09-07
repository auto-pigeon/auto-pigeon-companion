package pack

import (
	"strings"
	"testing"
)

// A path inside an archive is the only part of an archive that becomes a path
// on the reader's machine, and this build writes printable ASCII into one.
//
// That is a decision, not an omission — a PAK name field declares no encoding
// at all, ZIP has two and a flag that is often wrong, and case-collision
// detection is exactly correct on ASCII and merely plausible on Unicode. What
// matters for a user whose language is not English is that the refusal is a
// REFUSAL, naming the byte and where it is, and never a silent transliteration
// into a name they did not choose.
func TestANonASCIIMemberNameIsRefusedByNameAndNotTranslated(t *testing.T) {
	target := mustTarget(t, "quake3-pk3")
	for _, name := range []string{
		"maps/château.bsp",
		"textures/日本語/wall.tga",
		"sound/Ω-ambient.wav",
		"textures/ıstanbul.tga",
	} {
		err := CheckEntryPath(name, target.MaxNameLength)
		if err == nil {
			t.Errorf("%q was accepted; this build writes printable ASCII only", name)
			continue
		}
		if !strings.Contains(err.Error(), "printable ASCII") {
			t.Errorf("%q was refused for the wrong reason: %v", name, err)
		}
		// The offset, so a person can find the character in a long path rather
		// than being told the whole name is bad.
		if !strings.Contains(err.Error(), "at offset") {
			t.Errorf("the refusal of %q does not say where: %v", name, err)
		}
	}

	// The same rule on the way in, so an archive somebody else wrote cannot put
	// a name on this machine that this build would not have written.
	dir := t.TempDir()
	path, _ := writeArchive(t, dir, "ascii"+target.Extension(), []Member{
		memberOf("maps/e1m1.bsp", "fine"),
	}, target)
	if _, err := Inspect(path, target.Format, Budget{}); err != nil {
		t.Fatalf("an ASCII archive was refused: %v", err)
	}
}

// A NUL is the one non-ASCII byte with its own message, because it does not
// merely fail to be ASCII: it ends the name early for every engine that reads a
// PAK's name field as a C string, so `maps/e1m1.bsp\x00../../x` IS `maps/e1m1.bsp`
// to the engine and something else to the extractor.
func TestANULInAMemberNameIsCalledOutSeparately(t *testing.T) {
	err := CheckEntryPath("maps/e1m1.bsp\x00../../evil", PAKNameLength)
	if err == nil {
		t.Fatal("a NUL byte was accepted")
	}
	if !strings.Contains(err.Error(), "NUL") || !strings.Contains(err.Error(), "C string") {
		t.Errorf("the refusal does not explain what a NUL does: %v", err)
	}
}

// A member name longer than the format's field is refused by name, with the two
// numbers, and never silently truncated into a different file. Multi-byte
// characters make this easy to get wrong, because the limit is bytes.
func TestAnOverlongUnicodeNameIsRefusedRatherThanTruncated(t *testing.T) {
	target := mustTarget(t, "quake-pak")
	long := "maps/" + strings.Repeat("é", 400) + ".bsp"
	err := CheckEntryPath(long, target.MaxNameLength)
	if err == nil {
		t.Fatal("an overlong name was accepted")
	}
	if !strings.Contains(err.Error(), "over the") || !strings.Contains(err.Error(), "bytes") {
		t.Errorf("the refusal does not give the two numbers: %v", err)
	}
	// The length rule fires first: being told "this is 809 bytes and the field
	// holds 56" is more useful than being told about the first é.
	if strings.Contains(err.Error(), "printable ASCII") {
		t.Errorf("an overlong name was reported as an encoding problem: %v", err)
	}
}

// The case key is what makes two members that are one file on macOS a
// collision. It has no locale in it, which is only true because `İ` is not a
// byte this package accepts — the Turkish dotless i is the classic way a
// case-fold changes meaning between two machines.
func TestTheCaseKeyCannotBeReachedByALocaleDependentCharacter(t *testing.T) {
	if CaseKey("MAPS/E1M1.BSP") != "maps/e1m1.bsp" {
		t.Errorf("the case key is %q", CaseKey("MAPS/E1M1.BSP"))
	}
	if err := CheckEntryPath("maps/İstanbul.bsp", PK3NameLength); err == nil {
		t.Error("a character whose lowercase form depends on the locale was accepted")
	}
}
