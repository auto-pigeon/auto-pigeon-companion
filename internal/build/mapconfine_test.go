package build

// `NEW_342` lead `aucom/job/map-controlled-compiler-paths-unsanitised`, repaired
// in `NEW_348`: a map that came from Auto-Pigeon cannot make the compiler read a
// file outside the build.

import (
	"os"
	"strings"
	"testing"
)

const confinedWorld = `// Game: Quake
{
"classname" "worldspawn"
"wad" "/home/victim/private.wad;..\..\outside\rel.wad;gfx/base.wad;C:\quake\id1\gfx.wad;//server/share/net.wad;metal.wad"
// brush 0
{
( -64 -64 -16 ) ( -64 -63 -16 ) ( -64 -64 -15 ) sand [ 0 -1 0 0 ] [ 0 0 -1 0 ] 0 1 1
}
}
`

func TestConfiningAMapKeepsOnlyNamesInsideTheBuild(t *testing.T) {
	out, rewritten, err := confineMapText([]byte(confinedWorld))
	if err != nil {
		t.Fatal(err)
	}
	want := `"wad" "private.wad;rel.wad;gfx/base.wad;gfx.wad;net.wad;metal.wad"`
	if !strings.Contains(string(out), want) {
		t.Fatalf("the confined map does not carry %s:\n%s", want, out)
	}
	if len(rewritten) != 4 {
		t.Fatalf("rewritten = %v, want the four entries that pointed outside", rewritten)
	}
	// Everything else is byte-for-byte what it was: brushes, comments, order.
	if !strings.Contains(string(out), "( -64 -64 -16 ) ( -64 -63 -16 ) ( -64 -64 -15 ) sand") ||
		!strings.HasPrefix(string(out), "// Game: Quake\n{\n\"classname\" \"worldspawn\"\n") {
		t.Fatalf("confining changed more than the wad value:\n%s", out)
	}

	// A map that names nothing outside is returned untouched.
	clean := strings.Replace(confinedWorld, `"wad" "/home/victim/private.wad;..\..\outside\rel.wad;gfx/base.wad;C:\quake\id1\gfx.wad;//server/share/net.wad;metal.wad"`,
		`"_wad" "gfx/base.wad;metal.wad"`, 1)
	same, none, err := confineMapText([]byte(clean))
	if err != nil || len(none) != 0 || string(same) != clean {
		t.Fatalf("a clean map was changed (%v, %v)", none, err)
	}
}

// An external map is a file the compiler would read as map text from wherever
// the value points; nothing Auto-Pigeon stages is one, so it is refused by name.
func TestAnExternalMapReferenceIsRefused(t *testing.T) {
	for _, value := range []string{"/home/victim/private.map", "../../secret.map", "prefab.map"} {
		text := confinedWorld + "{\n\"classname\" \"misc_external_map\"\n\"_external_map\" \"" + value + "\"\n}\n"
		if _, _, err := confineMapText([]byte(text)); err == nil || !strings.Contains(err.Error(), "_external_map") {
			t.Fatalf("an _external_map of %q was not refused: %v", value, err)
		}
	}
}

// Only a key at entity level counts. A quoted string inside a brush (a Quake III
// shader name, say) or after a comment is not a key/value pair.
func TestConfiningReadsOnlyEntityKeys(t *testing.T) {
	text := "{\n\"classname\" \"worldspawn\"\n// \"wad\" \"/etc/not-a-key.wad\"\n{\nbrushDef\n{\n( 0 0 0 ) ( 1 0 0 ) ( 0 1 0 ) ( ( 1 0 0 ) ( 0 1 0 ) ) \"wad\" 0 0 0\n}\n}\n}\n"
	out, rewritten, err := confineMapText([]byte(text))
	if err != nil || len(rewritten) != 0 || string(out) != text {
		t.Fatalf("a quoted string outside entity level was treated as a key (%v, %v)", rewritten, err)
	}
}

// Through the runner: an input with a SourceRef — it came from Auto-Pigeon — is
// confined in the staged copy; a file the user pointed at on their own disk is
// theirs and is staged exactly as it is.
func TestAnAutoPigeonInputIsConfinedAndALocalOneIsNot(t *testing.T) {
	h := newHarness(t, standardFixtures(t))
	source := h.sourceMap("e1m1.map", confinedWorld)
	aub := map[string]SourceRef{"source_map": {
		Backend: "http://localhost:9190", AssetType: "map",
		AssetID: "map1", RevisionID: "rev1", Revision: 1, Refetchable: true,
	}}

	fromAUB := h.mustRun(Request{PipelineID: "test.build.pipeline",
		Inputs: map[string]string{"source_map": source}, Sources: aub})
	staged, err := os.ReadFile(inputNamed(t, fromAUB, "source_map").Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(staged), "/home/victim") || strings.Contains(string(staged), `..\`) {
		t.Fatalf("the staged Auto-Pigeon map still names files outside the build:\n%s", staged)
	}

	local := h.mustRun(Request{PipelineID: "test.build.pipeline",
		Inputs: map[string]string{"source_map": source}})
	untouched, err := os.ReadFile(inputNamed(t, local, "source_map").Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(untouched) != confinedWorld {
		t.Fatal("a local map the user chose was rewritten")
	}

	external := h.sourceMap("ext.map", confinedWorld+"{\n\"classname\" \"misc_external_map\"\n\"_external_map\" \"/home/victim/private.map\"\n}\n")
	if _, err := h.run(Request{PipelineID: "test.build.pipeline",
		Inputs: map[string]string{"source_map": external}, Sources: aub}); err == nil ||
		!strings.Contains(err.Error(), "_external_map") {
		t.Fatalf("an Auto-Pigeon map with an external map was compiled: %v", err)
	}
}

// The compiler's tokenizer reads past the end of a map that ends without a
// newline, inside a `//` comment, or inside a quoted string (ericw `parse_token`,
// `NEW_348` f14b — fixed in the fork, not yet in the pinned release). A map from
// Auto-Pigeon is therefore staged ending in a newline, and one whose quoted
// string is still open at the end of the file is refused.
func TestAMapFromAutoPigeonEndsWhereTheCompilerCanStop(t *testing.T) {
	for name, text := range map[string]string{
		"no final newline":   "{\n\"classname\" \"worldspawn\"\n}",
		"a trailing comment": "{\n\"classname\" \"worldspawn\"\n}\n// the end",
	} {
		out, rewritten, err := confineMapText([]byte(text))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasSuffix(string(out), "\n") || string(out) != text+"\n" || len(rewritten) != 1 {
			t.Fatalf("%s: staged %q (%v), want the same map ending in a newline", name, out, rewritten)
		}
	}

	for name, text := range map[string]string{
		"an open quote":                 "{\n\"classname\" \"worldspawn\"\n\"message\" \"never closed\n}\n",
		"a backslash as the last byte":  "{\n\"classname\" \"worldspawn\"\n\"message\" \"ends in \\",
		"an escaped quote that remains": "{\n\"classname\" \"worldspawn\"\n\"message\" \"x\\\" more\n}\n",
	} {
		if _, _, err := confineMapText([]byte(text)); err == nil || !strings.Contains(err.Error(), "quoted") {
			t.Fatalf("%s: was not refused (%v)", name, err)
		}
	}

	// ericw closes a quote at `\"` followed by a line break — an old editor's
	// `"wad" "c:\quake\"` — so that is not an open quote.
	oldEditor := "{\n\"classname\" \"worldspawn\"\n\"wad\" \"c:\\quake\\\"\n}\n"
	if _, _, err := confineMapText([]byte(oldEditor)); err != nil {
		t.Fatalf("an old editor's trailing-backslash value was refused: %v", err)
	}

	// A well-formed map is untouched, quotes in comments included.
	clean := "// \"a quote in a comment\n{\n\"classname\" \"worldspawn\"\n}\n"
	if out, rewritten, err := confineMapText([]byte(clean)); err != nil || len(rewritten) != 0 || string(out) != clean {
		t.Fatalf("a well-formed map was changed (%v, %v)", rewritten, err)
	}
}
