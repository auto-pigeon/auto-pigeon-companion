package q2deps

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2fixture"
)

// caseInsensitive reports whether the filesystem holding dir opens a file
// under another spelling of its name — measured with a sentinel this test owns,
// in the folder the fixture goes in, not assumed from the OS: macOS volumes,
// Windows folders and Linux casefold directories all differ from their
// platform's default, and a Windows folder can differ from its parent.
func caseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(dir, "Q2depsCaseSentinel")
	if err := os.WriteFile(probe, []byte{1}, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(probe)
	written, err := os.Stat(probe)
	if err != nil {
		t.Fatal(err)
	}
	other, err := os.Stat(filepath.Join(dir, "q2DEPScASEsENTINEL"))
	insensitive := err == nil && os.SameFile(written, other)
	t.Logf("%s is case-%s (measured)", dir, map[bool]string{true: "insensitive", false: "sensitive"}[insensitive])
	return insensitive
}

// mustWrite writes a fixture, and a fixture that could not be written fails
// the test there rather than as a wrong answer later.
func mustWrite(t *testing.T, root string, files map[string][]byte) string {
	t.Helper()
	if _, err := q2fixture.Write(root, files); err != nil {
		t.Fatalf("writing the fixture under %s: %v", root, err)
	}
	return root
}

// listing is every file under root as this host enumerates it, spelled as its
// directory entries are — what a failing case prints, so a folder the host
// merged or a file it replaced is in the log rather than inferred.
func listing(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			rel, _ := filepath.Rel(root, path)
			info, _ := entry.Info()
			size := int64(-1)
			if info != nil {
				size = info.Size()
			}
			lines = append(lines, fmt.Sprintf("  %s (%d bytes)", filepath.ToSlash(rel), size))
		}
		return nil
	})
	if err != nil {
		lines = append(lines, "  (listing stopped: "+err.Error()+")")
	}
	return strings.Join(lines, "\n")
}

// contentOf is the bytes a location holds: the loose file itself, or the
// member of the archive in index's folder, read from the PAK's own directory.
func contentOf(t *testing.T, index *Index, location Location) string {
	t.Helper()
	if location.Origin != Archived {
		return bytesOf(t, location)
	}
	data, err := os.ReadFile(filepath.Join(index.Root, location.Archive))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 12 || string(data[:4]) != "PACK" {
		t.Fatalf("%s is not a PAK", location.Archive)
	}
	offset, length := binary.LittleEndian.Uint32(data[4:]), binary.LittleEndian.Uint32(data[8:])
	for at := offset; at+64 <= offset+length && int(at+64) <= len(data); at += 64 {
		record := data[at : at+64]
		if cString(record[:56]) != location.Path {
			continue
		}
		start, size := binary.LittleEndian.Uint32(record[56:]), binary.LittleEndian.Uint32(record[60:])
		return string(data[start : start+size])
	}
	t.Fatalf("%s holds no member %q", location.Archive, location.Path)
	return ""
}

// foldingStat answers an open the way a case-insensitive filesystem does: each
// component of the path below root matches a directory entry whatever its
// case. It lets a case-sensitive test machine ask the question a Windows or
// macOS one answers. Only the components below root are folded: above it a
// Windows temporary folder can be named by its 8.3 short name (`RUNNER~1`),
// which no directory listing shows (Q2_020, windows-latest).
func foldingStat(root string) func(string) (os.FileInfo, error) {
	return func(path string) (os.FileInfo, error) {
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil, os.ErrNotExist
		}
		current := root
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if part == "" || part == "." {
				continue
			}
			entries, err := os.ReadDir(current)
			if err != nil {
				return nil, err
			}
			found := ""
			for _, entry := range entries {
				if strings.EqualFold(entry.Name(), part) {
					found = entry.Name()
					break
				}
			}
			if found == "" {
				return nil, os.ErrNotExist
			}
			current = filepath.Join(current, found)
		}
		return os.Stat(current)
	}
}

func resolveNamed(t *testing.T, index *Index, name string) Resolved {
	t.Helper()
	path := "textures/" + name + ".wal"
	report := Resolve(&Map{References: []Reference{{Kind: KindTexture, Name: name, Paths: []string{path}}}}, index, nil)
	return report.Files[0]
}

func bytesOf(t *testing.T, location Location) string {
	t.Helper()
	data, err := os.ReadFile(location.File)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// What a case-insensitive filesystem leaves when `textures/Mine/Upper.wal` and
// `textures/mine/lower.wal` are both written (CI at 7b7524d, macOS and
// Windows): `Mine/` and `mine/` are one folder, under whichever spelling was
// created first. The engine's own open of `textures/MINE/LOWER.wal` finds the
// file there; the index has to as well. Built directly in its merged shape and
// asked through the simulation, so a case-sensitive runner checks it too.
func TestAMergedFolderIsFoundWhereTheOperatingSystemOpensIt(t *testing.T) {
	content := mustWrite(t, filepath.Join(t.TempDir(), "content"), map[string][]byte{
		"textures/Mine/Upper.wal": {7},
		"textures/Mine/lower.wal": {8},
	})
	index, err := IndexDir(content)
	if err != nil {
		t.Fatal(err)
	}
	index.stat = foldingStat(content)
	for name, want := range map[string]string{
		"MINE/LOWER": "textures/Mine/lower.wal",
		"mine/lower": "textures/Mine/lower.wal",
		"Mine/Upper": "textures/Mine/Upper.wal",
		"mine/UPPER": "textures/Mine/Upper.wal",
	} {
		got := resolveNamed(t, index, name)
		if got.Where != FromContent || got.Location.Path != want ||
			bytesOf(t, got.Location) != map[string]string{"textures/Mine/lower.wal": "\x08", "textures/Mine/Upper.wal": "\x07"}[want] {
			t.Errorf("%s on a case-insensitive filesystem: %s %q, want content %q\n%s", name, got.Where, got.Location.Path, want, listing(t, content))
		}
	}
	// A name the folder does not hold under ANY spelling stays missing.
	if got := resolveNamed(t, index, "mine/absent"); got.Where != Missing {
		t.Errorf("mine/absent: %s %q", got.Where, got.Location.Path)
	}
}

// The same folder on a case-sensitive filesystem: Yamagi asks for the name as
// written and then lower-cased, and neither is `textures/Mine/lower.wal`. The
// review says missing because the engine will not find it either — the Linux
// answer does not become a case-insensitive one. The complementary answer, on
// a filesystem that folds, is TestAMergedFolderIsFoundWhereTheOperatingSystemOpensIt.
func TestOnACaseSensitiveFilesystemOnlyTheTwoSpellingsTheEngineTriesAreFound(t *testing.T) {
	content := filepath.Join(t.TempDir(), "content")
	if caseInsensitive(t, content) {
		t.Skip("measured: this filesystem is case-insensitive; the sensitive answer cannot be observed here")
	}
	mustWrite(t, content, map[string][]byte{
		"textures/Mine/Upper.wal": {7},
		"textures/Mine/lower.wal": {8},
	})
	index, err := IndexDir(content)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolveNamed(t, index, "MINE/LOWER"); got.Where != Missing {
		t.Errorf("MINE/LOWER: %s %q, want missing\n%s", got.Where, got.Location.Path, listing(t, content))
	}
	if got := resolveNamed(t, index, "Mine/Upper"); got.Where != FromContent || got.Location.Path != "textures/Mine/Upper.wal" ||
		bytesOf(t, got.Location) != "\x07" {
		t.Errorf("Mine/Upper: %s %q\n%s", got.Where, got.Location.Path, listing(t, content))
	}
}

// Two names that differ only in case, written one after the other with
// different bytes. Where the host keeps two files, each spelling the engine
// tries reaches its own; where it keeps one — the second write opened the
// first file — every spelling reaches that one file, under the name it was
// created with. Which kind of host this is, is measured, and both answers are
// asserted, so neither kind of runner passes by skipping.
func TestCaseOnlyTwinsAreTwoFilesExactlyWhereTheHostKeepsTwo(t *testing.T) {
	content := filepath.Join(t.TempDir(), "content")
	insensitive := caseInsensitive(t, content)
	mustWrite(t, content, map[string][]byte{"textures/e1/Sky.wal": {1}})
	mustWrite(t, content, map[string][]byte{"textures/e1/sky.wal": {2}})
	index, err := IndexDir(content)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{ // name -> spelling on disk, bytes
		"e1/Sky": {"textures/e1/Sky.wal", "\x01"},
		"E1/SKY": {"textures/e1/sky.wal", "\x02"},
		"e1/sKy": {"textures/e1/sky.wal", "\x02"},
		"e1/sky": {"textures/e1/sky.wal", "\x02"},
	}
	files := 2
	if insensitive {
		files = 1
		for name := range want {
			want[name] = [2]string{"textures/e1/Sky.wal", "\x02"}
		}
	}
	if index.LooseFiles() != files {
		t.Errorf("the host kept %d file(s), want %d\n%s", index.LooseFiles(), files, listing(t, content))
	}
	for name, w := range want {
		got := resolveNamed(t, index, name)
		if got.Where != FromContent || got.Location.Path != w[0] || bytesOf(t, got.Location) != w[1] {
			t.Errorf("%s: %s %q, want %q holding %q\n%s", name, got.Where, got.Location.Path, w[0], w[1], listing(t, content))
		}
	}
}

// On this machine's real filesystem, whichever kind it is: a name that differs
// from the file on disk only by case is found exactly when the operating
// system would open it.
func TestTheNativeFilesystemDecidesWhatAnotherSpellingOpens(t *testing.T) {
	content := filepath.Join(t.TempDir(), "content")
	insensitive := caseInsensitive(t, content)
	mustWrite(t, content, map[string][]byte{"textures/Base/Wall_A.wal": {9}})
	index, err := IndexDir(content)
	if err != nil {
		t.Fatal(err)
	}
	got := resolveNamed(t, index, "BASE/WALL_a")
	switch {
	case insensitive && (got.Where != FromContent || got.Location.Path != "textures/Base/Wall_A.wal" || bytesOf(t, got.Location) != "\x09"):
		t.Errorf("case-insensitive filesystem: BASE/WALL_a is %s %q, want the file\n%s", got.Where, got.Location.Path, listing(t, content))
	case !insensitive && got.Where != Missing:
		t.Errorf("case-sensitive filesystem: BASE/WALL_a is %s %q, want missing\n%s", got.Where, got.Location.Path, listing(t, content))
	}
}

// Yamagi lower-cases a name with C's `tolower` and never sets a locale, so its
// second try changes `A`-`Z` and nothing else: `textures/UML/\u00c4.wal` is
// retried as `textures/uml/\u00c4.wal`, never as `textures/uml/\u00e4.wal`.
// Whether the first try opens `\u00e4.wal` is the filesystem's answer — Windows,
// macOS and exFAT fold that letter — so on a case-sensitive one it is missing,
// and on a folding one (measured, or simulated) it is the file.
func TestOnlyASCIILettersAreLowerCasedTheWayTheEngineDoes(t *testing.T) {
	for in, want := range map[string]string{
		"":                        "",
		"textures/e1/wall.wal":    "textures/e1/wall.wal",
		"Textures/E1/WALL.WAL":    "textures/e1/wall.wal",
		"textures/UML/\u00c4.wal": "textures/uml/\u00c4.wal",
	} {
		if got := engineLower(in); got != want {
			t.Errorf("engineLower(%q) = %q, want %q", in, got, want)
		}
	}
	content := filepath.Join(t.TempDir(), "content")
	insensitive := caseInsensitive(t, content)
	mustWrite(t, content, map[string][]byte{"textures/uml/\u00e4.wal": {1}})
	for _, host := range []struct {
		name        string
		insensitive bool
		simulate    bool
	}{{"this filesystem", insensitive, false}, {"simulated case-insensitive filesystem", true, true}} {
		t.Run(host.name, func(t *testing.T) {
			index, err := IndexDir(content)
			if err != nil {
				t.Fatal(err)
			}
			if host.simulate {
				index.stat = foldingStat(content)
			}
			// The second try is exactly the file's name on every host.
			if got := resolveNamed(t, index, "UML/\u00e4"); got.Where != FromContent || bytesOf(t, got.Location) != "\x01" {
				t.Errorf("UML/\u00e4: %s %q\n%s", got.Where, got.Location.Path, listing(t, content))
			}
			got := resolveNamed(t, index, "UML/\u00c4")
			switch {
			case host.insensitive && (got.Where != FromContent || got.Location.Path != "textures/uml/\u00e4.wal"):
				t.Errorf("UML/\u00c4 on a case-insensitive filesystem: %s %q, want the file\n%s", got.Where, got.Location.Path, listing(t, content))
			case !host.insensitive && got.Where != Missing:
				t.Errorf("UML/\u00c4 on a case-sensitive filesystem: %s %q, want missing: the engine lower-cases ASCII only", got.Where, got.Location.Path)
			}
		})
	}
}

// Q2_020, found with the retail game on the 1.270 candidate: id Software's own
// baseq2/pak0.pak holds `models/monsters/tank/../ctank/skin.pcx`. The archive
// reader refuses a member like that — right for extracting one — and the base
// index then held NOTHING of pak0.pak, so every base texture of a real map was
// "in neither your content folder nor the base game". An engine matches member
// names literally, so a plain-path request never reaches that member: the
// index skips it, says so, and keeps the rest.
func TestABaseArchiveWithAnUnsafeMemberNameIsStillIndexed(t *testing.T) {
	base := mustWrite(t, filepath.Join(t.TempDir(), "baseq2"), map[string][]byte{
		"pak0.pak": q2fixture.PAK(
			[]string{"textures/e1u1/box3_1.wal", "models/monsters/tank/../ctank/skin.pcx", "env/unit1_rt.pcx"},
			map[string][]byte{"textures/e1u1/box3_1.wal": {1}, "models/monsters/tank/../ctank/skin.pcx": {2}, "env/unit1_rt.pcx": {3}}),
	})
	index, err := IndexDir(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"textures/e1u1/box3_1.wal", "env/unit1_rt.pcx"} {
		if location, ok := index.Find(path); !ok || location.Archive != "pak0.pak" {
			t.Errorf("%s: %+v %v, want it from pak0.pak", path, location, ok)
		}
	}
	if len(index.Archives) != 1 {
		t.Errorf("archives read: %v", index.Archives)
	}
	limits := strings.Join(index.Limits, "\n")
	if !strings.Contains(limits, "models/monsters/tank/../ctank/skin.pcx") || strings.Contains(limits, "could not be read") {
		t.Errorf("limits: %s", limits)
	}
}
