package q2deps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/q2fixture"
)

// caseInsensitive reports whether the filesystem holding dir opens a file
// under another spelling of its name — measured, not assumed from the OS:
// macOS volumes, Windows folders and Linux casefold directories all differ
// from their platform's default.
func caseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "CaseProbe")
	if err := os.WriteFile(probe, []byte{1}, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(probe)
	_, err := os.Stat(filepath.Join(dir, "caseprobe"))
	return err == nil
}

// foldingStat answers an open the way a case-insensitive filesystem does: each
// component of the path matches a directory entry whatever its case. It lets a
// case-sensitive test machine ask the question a Windows or macOS one answers.
func foldingStat(path string) (os.FileInfo, error) {
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path[len(volume):], string(filepath.Separator))
	current := volume + string(filepath.Separator)
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		if part == "" {
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

// What a case-insensitive filesystem leaves after the fixture of
// TestAReferenceResolvesTheWayTheEngineSearches: `Mine/` and `mine/` are one
// folder, under whichever spelling was created first. The engine's own open of
// `textures/MINE/LOWER.wal` finds the file there; the index has to as well.
func TestAMergedFolderIsFoundWhereTheOperatingSystemOpensIt(t *testing.T) {
	content, _ := q2fixture.Write(filepath.Join(t.TempDir(), "content"), map[string][]byte{
		"textures/Mine/Upper.wal": {7},
		"textures/Mine/lower.wal": {8},
	})
	index, err := IndexDir(content)
	if err != nil {
		t.Fatal(err)
	}
	index.stat = foldingStat
	for name, want := range map[string]string{
		"MINE/LOWER": "textures/Mine/lower.wal",
		"mine/lower": "textures/Mine/lower.wal",
		"Mine/Upper": "textures/Mine/Upper.wal",
		"mine/UPPER": "textures/Mine/Upper.wal",
	} {
		got := resolveNamed(t, index, name)
		if got.Where != FromContent || got.Location.Path != want {
			t.Errorf("%s on a case-insensitive filesystem: %s %q, want content %q", name, got.Where, got.Location.Path, want)
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
// answer does not become a case-insensitive one.
func TestOnACaseSensitiveFilesystemOnlyTheTwoSpellingsTheEngineTriesAreFound(t *testing.T) {
	dir := t.TempDir()
	if caseInsensitive(t, dir) {
		t.Skip("this filesystem is case-insensitive; the sensitive answer cannot be observed here")
	}
	content, _ := q2fixture.Write(filepath.Join(dir, "content"), map[string][]byte{
		"textures/Mine/Upper.wal": {7},
		"textures/Mine/lower.wal": {8},
		// Two files whose names differ only in case, with different bytes:
		// each spelling the engine tries reaches its own.
		"textures/e1/Sky.wal": {1},
		"textures/e1/sky.wal": {2},
	})
	index, err := IndexDir(content)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolveNamed(t, index, "MINE/LOWER"); got.Where != Missing {
		t.Errorf("MINE/LOWER: %s %q, want missing", got.Where, got.Location.Path)
	}
	if got := resolveNamed(t, index, "Mine/Upper"); got.Where != FromContent || got.Location.Path != "textures/Mine/Upper.wal" {
		t.Errorf("Mine/Upper: %s %q", got.Where, got.Location.Path)
	}
	for name, want := range map[string]string{"e1/Sky": "\x01", "E1/SKY": "\x02", "e1/sKy": "\x02", "e1/sky": "\x02"} {
		got := resolveNamed(t, index, name)
		if got.Where != FromContent || bytesOf(t, got.Location) != want {
			t.Errorf("%s: %s %q", name, got.Where, got.Location.Path)
		}
	}
}

// On this machine's real filesystem, whichever kind it is: a name that differs
// from the file on disk only by case is found exactly when the operating
// system would open it.
func TestTheNativeFilesystemDecidesWhatAnotherSpellingOpens(t *testing.T) {
	dir := t.TempDir()
	insensitive := caseInsensitive(t, dir)
	content, _ := q2fixture.Write(filepath.Join(dir, "content"), map[string][]byte{"textures/Base/Wall_A.wal": {9}})
	index, err := IndexDir(content)
	if err != nil {
		t.Fatal(err)
	}
	got := resolveNamed(t, index, "BASE/WALL_a")
	switch {
	case insensitive && (got.Where != FromContent || bytesOf(t, got.Location) != "\x09"):
		t.Errorf("case-insensitive filesystem: BASE/WALL_a is %s %q, want the file", got.Where, got.Location.Path)
	case !insensitive && got.Where != Missing:
		t.Errorf("case-sensitive filesystem: BASE/WALL_a is %s %q, want missing", got.Where, got.Location.Path)
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
	base, _ := q2fixture.Write(filepath.Join(t.TempDir(), "baseq2"), map[string][]byte{
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
