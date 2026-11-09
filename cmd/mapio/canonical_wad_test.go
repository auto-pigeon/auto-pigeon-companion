package main

// `NEW_440`: a worldspawn WAD list from Auto-Pigeon reaches qbsp only as plain
// `.wad` file names. NEW_434 reduced an entry that was absolute, had a `:` or
// climbed with `..` to `path.Base` of it — and `path.Base` splits on `/` only,
// so the drive-relative `C:secret.wad` came out as itself (NEW_436
// `extra:issue50-boundary`). POSIX reads that as a file called `C:secret.wad`
// inside the WAD folder; Windows reads it as `secret.wad` in drive C's current
// folder, wherever that is.
//
// The two path models below are the fixture's oracles, written from the
// compiler's code and not from the rule under test: ericw qbsp
// `LoadTexturePath` (`qbsp/map.cc`, fork release 1.4556) opens an absolute
// entry as it is and joins any other onto each `-wadpath` with
// `std::filesystem::path::operator/`; on Windows that join replaces the left
// side when the entry carries another root name (`C:`, `\\server`), keeps only
// the left side's drive when the entry carries a root directory (`\x`), and
// Win32 then folds `.`/`..` segments, drops the last segment's trailing dots
// and spaces, and opens a reserved device name as the device ("File path
// formats on Windows systems", .NET documentation: normalization). canonical_windows_test.go asks
// a real Windows for the same answers.

import (
	"path"
	"strings"
	"testing"
)

const (
	posixWADDir   = "/build/input/wads"
	windowsWADDir = `D:\build\input\wads`
)

// posixOpens is the file ericw opens on Linux or macOS for entry against dir.
func posixOpens(dir, entry string) string {
	if strings.HasPrefix(entry, "/") {
		return path.Clean(entry)
	}
	return path.Join(dir, entry)
}

// windowsDevices are the names Win32 opens as a device whatever folder they
// are joined to, with any extension.
var windowsDevices = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true, "CLOCK$": true,
}

func windowsDevice(name string) bool {
	stem := name
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
	stem = strings.ToUpper(strings.TrimRight(stem, " "))
	if windowsDevices[stem] {
		return true
	}
	return len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) &&
		stem[3] >= '0' && stem[3] <= '9'
}

// windowsOpens is the file ericw opens on Windows for entry against dir (a
// drive-letter folder): "" stands for "somewhere this model does not place",
// another drive's current folder, which is outside dir whatever it is.
func windowsOpens(dir, entry string) string {
	entry = strings.ReplaceAll(entry, "/", `\`)
	drive := strings.ToUpper(dir[:2])
	var joined string
	switch {
	case strings.HasPrefix(entry, `\\`):
		return entry // a UNC or device path: the entry replaces the folder
	case len(entry) >= 2 && entry[1] == ':':
		if strings.ToUpper(entry[:2]) != drive {
			return "" // another root name: that drive's own current folder
		}
		if rest := entry[2:]; strings.HasPrefix(rest, `\`) {
			joined = drive + rest
		} else {
			joined = dir + `\` + rest
		}
	case strings.HasPrefix(entry, `\`):
		joined = drive + entry // a root directory without a root name
	default:
		joined = dir + `\` + entry
	}
	var kept []string
	for _, segment := range strings.Split(joined[3:], `\`) {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(kept) > 0 {
				kept = kept[:len(kept)-1]
			}
			continue
		}
		kept = append(kept, segment)
	}
	if last := len(kept) - 1; last >= 0 {
		kept[last] = strings.TrimRight(kept[last], ". ")
		if windowsDevice(kept[last]) {
			return `\\.\` + kept[last]
		}
	}
	return joined[:3] + strings.Join(kept, `\`)
}

// insideAsNamed reports whether the opened file is the entry's own name directly
// inside dir, spelled as written: nothing above or beside the folder, and no
// device, stream or respelling in between.
func insideAsNamed(opened, dir, separator, entry string) bool {
	return opened != "" && opened == dir+separator+entry
}

// The models are the fixture's oracles, so they are pinned first: the vector
// NEW_436 found is inside the folder for POSIX and outside it for Windows.
func TestTheWADPathModelsReadTheCompilersWay(t *testing.T) {
	for _, c := range []struct{ entry, posix, windows string }{
		{"metal.wad", posixWADDir + "/metal.wad", windowsWADDir + `\metal.wad`},
		{"C:secret.wad", posixWADDir + "/C:secret.wad", ""},
		{"D:secret.wad", posixWADDir + "/D:secret.wad", windowsWADDir + `\secret.wad`},
		{`C:\quake\id1\gfx.wad`, posixWADDir + `/C:\quake\id1\gfx.wad`, ""},
		{`\root.wad`, posixWADDir + `/\root.wad`, `D:\root.wad`},
		{`\\server\share\net.wad`, posixWADDir + `/\\server\share\net.wad`, `\\server\share\net.wad`},
		{"//server/share/net.wad", "/server/share/net.wad", `\\server\share\net.wad`},
		{`..\..\x.wad`, posixWADDir + `/..\..\x.wad`, `D:\build\x.wad`},
		{"../../x.wad", "/build/x.wad", `D:\build\x.wad`},
		{"x.wad.", posixWADDir + "/x.wad.", windowsWADDir + `\x.wad`},
		{"NUL.wad", posixWADDir + "/NUL.wad", `\\.\NUL.wad`},
		{"/abs/x.wad", "/abs/x.wad", `D:\abs\x.wad`},
	} {
		if got := posixOpens(posixWADDir, c.entry); got != c.posix {
			t.Errorf("POSIX %q: %q, want %q", c.entry, got, c.posix)
		}
		if got := windowsOpens(windowsWADDir, c.entry); got != c.windows {
			t.Errorf("Windows %q: %q, want %q", c.entry, got, c.windows)
		}
	}
}

// wadOf is worldspawn's `wad` in the copy, as ericw reads it.
func wadOf(t *testing.T, out []byte) string {
	t.Helper()
	value, _ := ericwEntities(string(out))[0].get("wad")
	return value
}

// Every WAD entry of a map from Auto-Pigeon is one plain `.wad` file name in
// the copy, which both a POSIX and a Windows qbsp open directly inside the WAD
// folder; a folder entry opens nothing; anything else refuses the map.
func TestAWADEntryIsOnePlainFileNameOnEveryPlatform(t *testing.T) {
	reduced := []struct{ entry, name string }{
		{"C:secret.wad", "secret.wad"}, // NEW_436: kept verbatim by NEW_434
		{"d:x.wad", "x.wad"},
		{`C:\quake\id1\gfx.wad`, "gfx.wad"},
		{"c:/quake/id1/gfx.wad", "gfx.wad"},
		{`\\server\share\net.wad`, "net.wad"},
		{"//server/share/net.wad", "net.wad"},
		{`\\?\C:\long\path.wad`, "path.wad"},
		{`\\.\pipe\pipe.wad`, "pipe.wad"},
		{`\root.wad`, "root.wad"},
		{"/home/victim/private.wad", "private.wad"},
		{`..\..\outside\rel.wad`, "rel.wad"},
		{"../../up.wad", "up.wad"},
		{`.. \.. \dots.wad`, "dots.wad"},
		{"gfx/base.wad", "base.wad"},
		{"file:///C:/x/scheme.wad", "scheme.wad"},
		{"host.txt:stream.wad", "stream.wad"},
		{`C:\x\ spaced.wad`, "spaced.wad"},
	}
	kept := []string{"metal.wad", "METAL.WAD", "my textures.wad", "q1-base_2.Wad", "a..wad", "con1.wad", "comx.wad"}

	var entries, want []string
	for _, c := range reduced {
		entries, want = append(entries, c.entry), append(want, c.name)
	}
	entries, want = append(entries, kept...), append(want, kept...)
	out, changed, err := canonicalMapText([]byte(world(`"wad" "`+strings.Join(entries, ";")+`"`+"\n", box("sand"))))
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	bothModelsAgree(t, "wad list", out)
	if got := wadOf(t, out); got != strings.Join(want, ";") {
		t.Fatalf("wad =\n%q\nwant\n%q", got, strings.Join(want, ";"))
	}
	if len(changed) != len(reduced) {
		t.Fatalf("reported %d reduced entries, want %d: %q", len(changed), len(reduced), changed)
	}
	for index, c := range reduced {
		if changed[index] != c.entry {
			t.Fatalf("reduced entry %d reported as %q, want %q", index, changed[index], c.entry)
		}
	}
	for _, name := range want {
		if strings.ContainsAny(name, `/\:`) {
			t.Fatalf("%q survived the copy as a path", name)
		}
		if opened := posixOpens(posixWADDir, name); !insideAsNamed(opened, posixWADDir, "/", name) {
			t.Fatalf("POSIX opens %q for %q", opened, name)
		}
		if opened := windowsOpens(windowsWADDir, name); !insideAsNamed(opened, windowsWADDir, `\`, name) {
			t.Fatalf("Windows opens %q for %q", opened, name)
		}
	}

	// A folder is not a WAD: an old editor's `c:\quake\`, a lone drive, `..`.
	// Nothing is opened for it, and the map still builds.
	for _, folder := range []string{`c:\quake\`, "gfx/", "C:", "..", ".", `..\`, "/"} {
		out, changed, err := canonicalMapText([]byte(world(`"wad" "metal.wad;`+folder+`"`+"\n", box("sand"))))
		if err != nil {
			t.Fatalf("the folder entry %q refused the map: %v", folder, err)
		}
		if got := wadOf(t, out); got != "metal.wad" || len(changed) != 1 || changed[0] != folder {
			t.Fatalf("the folder entry %q: wad %q, reported %q", folder, got, changed)
		}
	}

	// Everything else stops the build: no name that Windows reads as a device
	// or a stream, no archive of another kind, nothing that is not one plain
	// file name either system spells the same way.
	for _, entry := range []string{
		"q.wad:stream", "x.wad::$DATA", // an alternate data stream of a WAD
		"NUL.wad", "con.wad", "Aux.Wad", "COM1.wad", "lpt9.wad", "prn .wad", "CONIN$.wad", "nul.tar.wad",
		"pak0.pak", "secret", "x.wad.", // another kind of file, or a name Windows respells
		".wad", ".hidden.wad",
		"a?.wad", "a*.wad", "a<b.wad", "a>b.wad", "a|b.wad",
		"caf\u00e9.wad", "x\uff0fy.wad", "x\u2215y.wad", // non-ASCII, including slash look-alikes
		strings.Repeat("w", 252) + ".wad",
	} {
		mustRefuse(t, entry, world(`"wad" "metal.wad;`+entry+`"`+"\n", box("sand")), "WAD")
		mustRefuse(t, "_wad "+entry, world(`"_WAD" "`+entry+`"`+"\n", box("sand")), "WAD")
	}
	// The limit is a limit, not a truncation: 255 bytes is a name.
	mustCanonical(t, "255-byte name", world(`"wad" "`+strings.Repeat("w", 251)+`.wad"`+"\n", box("sand")))
}

// The vector itself: the raw bytes hand qbsp an entry Windows opens outside the
// WAD folder and POSIX opens inside it; the copy hands it a name both open
// inside. A build without the boundary would have compiled the raw bytes.
func TestTheDriveRelativeWADEntryDoesNotSurviveTheCopy(t *testing.T) {
	raw := world(`"wad" "C:secret.wad;D:x.wad;metal.wad"`+"\n", box("sand"))
	rawEntries := strings.Split(wadOf(t, []byte(raw)), ";")
	if opened := windowsOpens(windowsWADDir, rawEntries[0]); opened != "" {
		t.Fatalf("the raw entry is not the vector: Windows opens %q", opened)
	}
	if opened := posixOpens(posixWADDir, rawEntries[0]); !insideAsNamed(opened, posixWADDir, "/", rawEntries[0]) {
		t.Fatalf("POSIX opens %q for the raw entry", opened)
	}
	out := mustCanonical(t, "drive-relative", raw)
	if got := wadOf(t, out); got != "secret.wad;x.wad;metal.wad" {
		t.Fatalf("wad = %q", got)
	}
}

// ericw keeps an escaped quote inside a string unless a line break follows it,
// Q3Map2 closes the string there: the WAD list cannot use that to carry a path
// past the copy.
func TestAnEscapedQuoteCannotCarryAPathIntoTheWADList(t *testing.T) {
	vector := "{\n\"classname\" \"worldspawn\"\n" +
		`"wad" "metal.wad;C:\quake\" "message" "C:secret.wad"` + "\n" + box("sand") + "}\n"
	if value, _ := ericwEntities(vector)[0].get("wad"); !strings.Contains(value, `"`) {
		t.Fatalf("the vector does not open a string only ericw sees: %q", value)
	}
	out := mustCanonical(t, "escaped quote", vector)
	if got := wadOf(t, out); got != "metal.wad" {
		t.Fatalf("wad = %q", got)
	}
	if got, _ := ericwEntities(string(out))[0].get("message"); got != "C:secret.wad" {
		t.Fatalf("the message key changed: %q", got)
	}
	// A value that ends in a backslash before its closing quote, followed by
	// a line break, closes in both: the entry is a folder, dropped.
	out = mustCanonical(t, "trailing backslash", world(`"wad" "metal.wad;C:\quake\"`+"\n", box("sand")))
	if got := wadOf(t, out); got != "metal.wad" {
		t.Fatalf("wad = %q", got)
	}
}

// qbsp's own settings that name a file or a folder of WADs are refused on
// worldspawn even though qbsp 1.4556 does not apply worldspawn keys as
// settings: only light does today, and the day qbsp does they would be live.
func TestWorldspawnCannotNameTheCompilersWADFoldersOrDefinitionFiles(t *testing.T) {
	for _, key := range []string{"wadpath", "_wadpath", "XWADPATH", "_xwadpath", "aliasdef", "_AliasDef",
		"texturedefs", "__texturedefs", "add", "_add"} {
		for _, value := range []string{`C:\quake\id1`, "/home/victim", "defs.def", "other.map"} {
			mustRefuse(t, key+"="+value, world("\""+key+"\" \""+value+"\"\n", box("sand")), "worldspawn")
		}
	}
	// An empty one names nothing, and on another entity the key is that
	// entity's own.
	mustCanonical(t, "empty wadpath", world("\"_wadpath\" \"\"\n", box("sand")))
	out := mustCanonical(t, "entity add", world("", box("sand"))+"{\n\"classname\" \"info_null\"\n\"add\" \"1\"\n}\n")
	if value, _ := ericwEntities(string(out))[1].get("add"); value != "1" {
		t.Fatalf("an entity's own add key was changed: %q", value)
	}
}
