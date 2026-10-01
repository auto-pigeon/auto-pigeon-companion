package q3vfs

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
)

func write(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// pk3 is a small valid archive: two stored members under `textures/`.
func pk3(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range []string{"textures/aucom/a.tga", "textures/aucom/b.tga"} {
		member, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write(bytes.Repeat([]byte(name), 40)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// roots lays out a base game folder and a content folder the way a user has
// them: each holds a `baseq3`, the content folder also holds a mod and, beside
// both, things a build has no business reading.
func roots(t *testing.T) (game, content string) {
	t.Helper()
	game, content = t.TempDir(), t.TempDir()
	write(t, filepath.Join(game, "baseq3", "pak0.pk3"), pk3(t))
	write(t, filepath.Join(game, "ioquake3.x86_64"), []byte("an engine, not content"))
	write(t, filepath.Join(game, "othermod", "pak9.pk3"), pk3(t))
	write(t, filepath.Join(content, "baseq3", "scripts", "aucom.shader"), []byte("textures/aucom/wall\n{\n}\n"))
	write(t, filepath.Join(content, "baseq3", "textures", "aucom", "wall.tga"), []byte("tga"))
	write(t, filepath.Join(content, "baseq3", ".git", "config"), []byte("[core]"))
	write(t, filepath.Join(content, "mymod", "textures", "mymod", "x.tga"), []byte("tga"))
	write(t, filepath.Join(content, "private-notes.txt"), []byte("not content"))
	return game, content
}

func build(t *testing.T, fsGame, game, content string) (*Stage, string, error) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "vfs")
	stage, err := Build(Request{
		FSGame: fsGame,
		Roots:  map[string]string{"game_root": game, "content_root": content},
		Dir:    dir,
	})
	return stage, dir, err
}

func staged(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			relative, _ := filepath.Rel(dir, path)
			out = append(out, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// What a build may read is the base game directory and the mod directory of
// each approved folder, and nothing beside them: not the engine binary in the
// game folder, not another mod, not a file next to `baseq3`, not `.git`.
func TestOnlyTheNamedGameDirectoriesAreStaged(t *testing.T) {
	game, content := roots(t)
	stage, dir, err := build(t, "mymod", game, content)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(staged(t, dir), "\n")
	want := strings.Join([]string{
		"content_root/baseq3/scripts/aucom.shader",
		"content_root/baseq3/textures/aucom/wall.tga",
		"content_root/mymod/textures/mymod/x.tga",
		"game_root/baseq3/pak0.pk3",
	}, "\n")
	if got != want {
		t.Fatalf("staged:\n%s\nwant:\n%s", got, want)
	}
	if stage.FSGame != "mymod" || stage.BaseGame != "baseq3" {
		t.Errorf("stage names %q / %q", stage.BaseGame, stage.FSGame)
	}
	paths := stage.Paths()
	if paths["game_root"] != filepath.Join(dir, "game_root") || paths["content_root"] != filepath.Join(dir, "content_root") {
		t.Errorf("staged paths = %v", paths)
	}

	// The archive is identified, and the staged file reads as the original.
	var archive Archive
	for _, root := range stage.Roots {
		for _, g := range root.Games {
			if root.Role == "game_root" && g.Name == "baseq3" && len(g.Archives) == 1 {
				archive = g.Archives[0]
			}
			if root.Role == "content_root" && g.Name == "baseq3" {
				if g.LooseFiles != 2 || len(g.Skipped) != 1 || g.Skipped[0].Path != ".git" {
					t.Errorf("content baseq3: %d loose, skipped %v", g.LooseFiles, g.Skipped)
				}
			}
		}
	}
	if archive.Name != "pak0.pk3" || archive.Entries != 2 || !strings.HasPrefix(archive.SHA256, "sha256:") {
		t.Errorf("archive record = %+v", archive)
	}
	original, _ := os.ReadFile(filepath.Join(game, "baseq3", "pak0.pk3"))
	through, err := os.ReadFile(filepath.Join(dir, "game_root", "baseq3", "pak0.pk3"))
	if err != nil || !bytes.Equal(original, through) {
		t.Errorf("the staged archive does not read as the original: %v", err)
	}
}

// Measured: `-fs_game ..` made Q3Map2 read the parent of every base path. A mod
// directory name is a name.
func TestAModDirectoryNameIsAName(t *testing.T) {
	game, content := roots(t)
	for _, name := range []string{"..", ".", "...", ".hidden", "a/b", `a\b`, "a b", strings.Repeat("m", 65)} {
		_, dir, err := build(t, name, game, content)
		if failure.Of(err) != failure.FSGameInvalid {
			t.Errorf("fs_game %q: class = %q (%v), want %s", name, failure.Of(err), err, failure.FSGameInvalid)
		}
		if _, statErr := os.Stat(dir); statErr == nil {
			t.Errorf("fs_game %q: something was staged before the refusal", name)
		}
	}
	for _, name := range []string{"", "mymod", "baseq3", "BaseQ3"} {
		if _, _, err := build(t, name, game, content); err != nil {
			t.Errorf("fs_game %q was refused: %v", name, err)
		}
	}
}

// Measured: Q3Map2 initialises a mod directory that does not exist and says
// nothing. Here it is the reason not to start.
func TestAModNoApprovedFolderHasIsRefused(t *testing.T) {
	game, content := roots(t)
	_, _, err := build(t, "nosuchmod", game, content)
	if failure.Of(err) != failure.FSGameNotFound {
		t.Fatalf("class = %q (%v), want %s", failure.Of(err), err, failure.FSGameNotFound)
	}
	// `othermod` exists in the game folder: found there, it is staged from
	// there, and the content folder simply has none.
	stage, _, err := build(t, "othermod", game, content)
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range stage.Roots {
		for _, g := range root.Games {
			if g.Name == "othermod" && g.Present != (root.Role == "game_root") {
				t.Errorf("%s/othermod present = %t", root.Role, g.Present)
			}
		}
	}
}

// A folder that holds no game directory is the wrong folder — usually `baseq3`
// itself, or its parent's parent — and it is said rather than compiled around.
func TestAFolderWithNoGameDirectoryIsMissingGameData(t *testing.T) {
	game, content := roots(t)
	_, _, err := build(t, "", filepath.Join(game, "baseq3"), content)
	if failure.Of(err) != failure.GameDataMissing {
		t.Fatalf("class = %q (%v), want %s", failure.Of(err), err, failure.GameDataMissing)
	}
	if !strings.Contains(err.Error(), "CONTAINS baseq3") {
		t.Errorf("the refusal does not say which folder to choose: %v", err)
	}
	_, _, err = build(t, "", filepath.Join(game, "nowhere"), content)
	if failure.Of(err) != failure.GameDataMissing {
		t.Fatalf("an absent folder: class = %q (%v)", failure.Of(err), err)
	}
}

// An empty base game directory is not refused — a map that uses only its own
// textures builds without the base game — and it is not passed over either.
func TestAnEmptyBaseGameIsAFindingNotARefusal(t *testing.T) {
	_, content := roots(t)
	game := t.TempDir()
	if err := os.MkdirAll(filepath.Join(game, "baseq3"), 0o755); err != nil {
		t.Fatal(err)
	}
	stage, _, err := build(t, "", game, content)
	if err != nil {
		t.Fatal(err)
	}
	if len(stage.Findings) != 1 || stage.Findings[0].Class != failure.GameDataMissing {
		t.Fatalf("findings = %+v", stage.Findings)
	}
	if !strings.Contains(stage.Findings[0].Message, "pak0.pk3") {
		t.Errorf("the finding does not name what is missing: %s", stage.Findings[0].Message)
	}
}

// Measured: a PK3 truncated to 700 bytes is skipped in silence and the build
// exits 0 with every image it carried "missing". Each damaged shape is refused
// by name, and nothing after it is staged for a build to read.
func TestADamagedArchiveIsRefusedByName(t *testing.T) {
	whole := pk3(t)
	for name, body := range map[string][]byte{
		"truncated":   whole[:len(whole)/2],
		"empty":       {},
		"not a zip":   []byte("this is a text file with a .pk3 name\n"),
		"header only": whole[:30],
	} {
		t.Run(name, func(t *testing.T) {
			game, content := roots(t)
			write(t, filepath.Join(content, "baseq3", "zz-broken.pk3"), body)
			_, _, err := build(t, "", game, content)
			if failure.Of(err) != failure.ArchiveDamaged {
				t.Fatalf("class = %q (%v), want %s", failure.Of(err), err, failure.ArchiveDamaged)
			}
			if !strings.Contains(err.Error(), "zz-broken.pk3") {
				t.Errorf("the refusal does not name the archive: %v", err)
			}
		})
	}
}

// A PK3 below the game directory's top level is not an archive to any engine:
// it is a file, staged as one, and not opened.
func TestOnlyATopLevelPK3IsAnArchive(t *testing.T) {
	game, content := roots(t)
	write(t, filepath.Join(content, "baseq3", "backup", "old.pk3"), []byte("not even a zip"))
	stage, _, err := build(t, "", game, content)
	if err != nil {
		t.Fatalf("a nested .pk3 was read as an archive: %v", err)
	}
	for _, root := range stage.Roots {
		if root.Role != "content_root" {
			continue
		}
		if g := root.Games[0]; len(g.Archives) != 0 || g.LooseFiles != 3 {
			t.Errorf("content baseq3: %d archives, %d loose", len(g.Archives), g.LooseFiles)
		}
	}
}

// A link out of the approved folders is a path nobody approved. A link that
// lands inside ANOTHER approved folder is fine: the user approved both.
func TestALinkOutOfTheApprovedFoldersIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symbolic link needs a privilege on Windows")
	}
	game, content := roots(t)
	outside := filepath.Join(t.TempDir(), "secret.tga")
	write(t, outside, []byte("somebody else's file"))
	link := filepath.Join(content, "baseq3", "textures", "aucom", "stolen.tga")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	_, _, err := build(t, "", game, content)
	if failure.Of(err) != failure.ContentRefused {
		t.Fatalf("class = %q (%v), want %s", failure.Of(err), err, failure.ContentRefused)
	}
	if !strings.Contains(err.Error(), "stolen.tga") {
		t.Errorf("the refusal does not name the link: %v", err)
	}

	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(game, "baseq3", "pak0.pk3"), filepath.Join(content, "baseq3", "pak0.pk3")); err != nil {
		t.Fatal(err)
	}
	stage, _, err := build(t, "", game, content)
	if err != nil {
		t.Fatalf("a link into the other approved folder was refused: %v", err)
	}
	for _, root := range stage.Roots {
		if root.Role == "content_root" && len(root.Games[0].Archives) != 1 {
			t.Errorf("the linked archive was not staged: %+v", root.Games[0])
		}
	}

	// A game directory that is itself a link out is the same thing, one level up.
	elsewhere := t.TempDir()
	write(t, filepath.Join(elsewhere, "textures", "x.tga"), []byte("tga"))
	linked := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(linked, "baseq3")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := build(t, "", game, linked); failure.Of(err) != failure.ContentRefused {
		t.Fatalf("a linked game directory: class = %q (%v)", failure.Of(err), err)
	}
}

// bound writes an archive where a verified cache would hold it — under a name
// that is NOT the archive's — and returns the binding for it.
func bound(t *testing.T, root, name string, body []byte) Package {
	t.Helper()
	path := filepath.Join(t.TempDir(), "objects", "ab", "cd", "the-digest-is-the-name")
	write(t, path, body)
	archive, err := inspectArchive(path, int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	return Package{Root: root, ArchiveName: name, SHA256: archive.SHA256, Path: path, Origin: OriginCache}
}

func buildWith(t *testing.T, fsGame string, roots map[string]string, packages ...Package) (*Stage, string, error) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "vfs")
	stage, err := Build(Request{FSGame: fsGame, Roots: roots, Dir: dir, Packages: packages})
	return stage, dir, err
}

// Q3_010: the packages a saved map is bound to are staged by the Companion —
// under the folder and the NAME the map gives, standing for the verified bytes
// — and recorded with the digest they were verified at.
func TestTheMapsBoundPackagesAreStagedByNameAndDigest(t *testing.T) {
	game, _ := roots(t)
	archive := pk3(t)
	pkg := bound(t, "baseq3", "zz_synth.pk3", archive)

	// No content folder at all: the map's own packages are its content.
	stage, dir, err := buildWith(t, "", map[string]string{"game_root": game}, pkg)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(filepath.Join(dir, "content_root", "baseq3", "zz_synth.pk3"))
	if err != nil || !bytes.Equal(staged, archive) {
		t.Fatalf("the bound package was not staged under its own name: %v", err)
	}
	if len(stage.Packages) != 1 {
		t.Fatalf("packages = %+v", stage.Packages)
	}
	record := stage.Packages[0]
	if !record.Staged || record.SHA256 != pkg.SHA256 || record.Entries != 2 || record.Size != int64(len(archive)) {
		t.Errorf("record = %+v", record)
	}
	if stage.Paths()["content_root"] != filepath.Join(dir, "content_root") {
		t.Errorf("the content root was not created for the packages: %v", stage.Paths())
	}
}

func TestABoundPackageAndAFolderArchiveOfTheSameName(t *testing.T) {
	game, content := roots(t)
	archive := pk3(t)
	both := map[string]string{"game_root": game, "content_root": content}

	// The same bytes under the same name in the user's folder: one archive.
	write(t, filepath.Join(content, "baseq3", "zz_synth.pk3"), archive)
	stage, _, err := buildWith(t, "", both, bound(t, "baseq3", "zz_synth.pk3", archive))
	if err != nil {
		t.Fatalf("the same archive in the folder and in the binding was refused: %v", err)
	}
	for _, root := range stage.Roots {
		if root.Role == "content_root" && len(root.Games[0].Archives) != 1 {
			t.Errorf("content archives = %+v", root.Games[0].Archives)
		}
	}

	// Different bytes under that name: neither may silently win.
	write(t, filepath.Join(content, "baseq3", "zz_synth.pk3"), append(pk3(t), []byte("PK\x05\x06")...))
	_, _, err = buildWith(t, "", both, bound(t, "baseq3", "zz_synth.pk3", archive))
	if err == nil || (failure.Of(err) != failure.ContentRefused && failure.Of(err) != failure.ArchiveDamaged) {
		t.Fatalf("err = %v (class %q)", err, failure.Of(err))
	}
}

func TestABoundPackageThatIsNotTheBytesItClaimsIsRefused(t *testing.T) {
	game, _ := roots(t)
	pkg := bound(t, "baseq3", "zz_synth.pk3", pk3(t))
	pkg.SHA256 = "sha256:" + strings.Repeat("0", 64)
	if _, _, err := buildWith(t, "", map[string]string{"game_root": game}, pkg); failure.Of(err) != failure.ArchiveDamaged {
		t.Fatalf("a digest that does not match: %v (class %q)", err, failure.Of(err))
	}

	garbage := bound(t, "baseq3", "zz_synth.pk3", pk3(t))
	if err := os.WriteFile(garbage.Path, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildWith(t, "", map[string]string{"game_root": game}, garbage); failure.Of(err) != failure.ArchiveDamaged {
		t.Fatalf("a bound file that is not an archive: %v (class %q)", err, failure.Of(err))
	}

	path := bound(t, "baseq3", "../escape.pk3", pk3(t))
	if _, dir, err := buildWith(t, "", map[string]string{"game_root": game}, path); failure.Of(err) != failure.ContentRefused {
		t.Fatalf("an archive name that is a path: %v (class %q)", err, failure.Of(err))
	} else if _, statErr := os.Stat(filepath.Join(dir, "content_root", "escape.pk3")); statErr == nil {
		t.Error("the archive was written outside its game folder")
	}
}

// A package bound under a mod folder makes that mod exist for this build; one
// bound under a folder the build does not read is recorded as not staged.
func TestABoundPackageUnderAModFolder(t *testing.T) {
	game, _ := roots(t)
	only := map[string]string{"game_root": game}
	stage, dir, err := buildWith(t, "packmod", only, bound(t, "packmod", "zz_mod.pk3", pk3(t)))
	if err != nil {
		t.Fatalf("a mod that exists only as the map's packages was not found: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "content_root", "packmod", "zz_mod.pk3")); err != nil {
		t.Errorf("not staged under the mod folder: %v", err)
	}
	if !stage.Packages[0].Staged {
		t.Errorf("record = %+v", stage.Packages[0])
	}

	stage, dir, err = buildWith(t, "", only, bound(t, "elsewhere", "zz_other.pk3", pk3(t)))
	if err != nil {
		t.Fatal(err)
	}
	if stage.Packages[0].Staged || !strings.Contains(stage.Packages[0].Reason, "elsewhere") {
		t.Errorf("record = %+v", stage.Packages[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "content_root", "elsewhere")); err == nil {
		t.Error("a folder the build does not read was staged")
	}
	found := false
	for _, finding := range stage.Findings {
		if strings.Contains(finding.Message, "zz_other.pk3") {
			found = true
		}
	}
	if !found {
		t.Errorf("no finding says the package was not shown to the compiler: %+v", stage.Findings)
	}
}
