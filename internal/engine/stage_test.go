package engine_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/engine"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(body)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// A project directory, somewhere that is not the game.
func project(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "maps", "level.bsp"), "BSP")
	writeFile(t, filepath.Join(dir, "maps", "level.lit"), "LIT")
	writeFile(t, filepath.Join(dir, "progs.dat"), "PROGS")
	return dir
}

func gameRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "id1", "pak0.pak"), "PACK")
	return dir
}

func TestStageCopiesAndUnstageRemovesExactlyWhatItWrote(t *testing.T) {
	game, source := gameRoot(t), project(t)
	staging := engine.Staging{GameRoot: game, ModName: "mymap", Source: source}

	planned, err := staging.Plan()
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	if len(planned) != 3 {
		t.Fatalf("planned %v, want three files", planned)
	}

	stamp, err := staging.Stage()
	if err != nil {
		t.Fatalf("staging: %v", err)
	}
	if len(stamp.Files) != 3 || !stamp.CreatedDir {
		t.Fatalf("stamp = %+v", stamp)
	}
	if got := readFile(t, filepath.Join(game, "mymap", "maps", "level.bsp")); got != "BSP" {
		t.Errorf("the staged map is %q", got)
	}

	kept, err := engine.Unstage(game, "mymap")
	if err != nil {
		t.Fatalf("unstaging: %v", err)
	}
	if len(kept) != 0 {
		t.Errorf("unstaging kept %v", kept)
	}
	if exists(filepath.Join(game, "mymap")) {
		t.Error("the game directory the Companion created is still there")
	}
	// The user's own files were read and never touched.
	if got := readFile(t, filepath.Join(source, "maps", "level.bsp")); got != "BSP" {
		t.Errorf("the project's own map changed: %q", got)
	}
	if !exists(filepath.Join(game, "id1", "pak0.pak")) {
		t.Error("cleanup reached the base game")
	}
}

// The rule that matters: cleanup removes what it wrote, and stops at anything
// that has changed. A user who edited a staged file, or dropped one of their
// own in beside it, keeps it.
func TestUnstageLeavesFilesThatChangedAndFilesItNeverWrote(t *testing.T) {
	game, source := gameRoot(t), project(t)
	if _, err := (engine.Staging{GameRoot: game, ModName: "mymap", Source: source}).Stage(); err != nil {
		t.Fatalf("staging: %v", err)
	}
	edited := filepath.Join(game, "mymap", "progs.dat")
	writeFile(t, edited, "EDITED BY HAND")
	mine := filepath.Join(game, "mymap", "config.cfg")
	writeFile(t, mine, "bind x impulse 9")

	kept, err := engine.Unstage(game, "mymap")
	if err != nil {
		t.Fatalf("unstaging: %v", err)
	}
	if len(kept) != 1 || kept[0] != "progs.dat" {
		t.Errorf("kept %v, want the one file that had changed", kept)
	}
	if got := readFile(t, edited); got != "EDITED BY HAND" {
		t.Errorf("a changed file was overwritten or removed: %q", got)
	}
	if !exists(mine) {
		t.Error("a file the Companion never wrote was removed")
	}
	if exists(filepath.Join(game, "mymap", "maps", "level.bsp")) {
		t.Error("an unchanged staged file survived cleanup")
	}
	if !exists(filepath.Join(game, "mymap")) {
		t.Error("the directory was removed although the user still had files in it")
	}
}

// A directory somebody else made is not one to write into, and there is no
// flag that makes it one.
func TestStageRefusesADirectoryItDidNotCreate(t *testing.T) {
	game, source := gameRoot(t), project(t)
	writeFile(t, filepath.Join(game, "mymap", "notes.txt"), "six months of work")

	_, err := (engine.Staging{GameRoot: game, ModName: "mymap", Source: source}).Stage()
	if !errors.Is(err, engine.ErrOccupied) {
		t.Fatalf("staging into somebody else's directory returned %v", err)
	}
	if got := readFile(t, filepath.Join(game, "mymap", "notes.txt")); got != "six months of work" {
		t.Errorf("the refusal was not clean: %q", got)
	}
}

// Re-staging replaces what the last run put there, so a map renamed between
// two runs does not leave the old one behind for the engine to load instead.
func TestStagingTwiceReplacesTheEarlierCopy(t *testing.T) {
	game, source := gameRoot(t), project(t)
	staging := engine.Staging{GameRoot: game, ModName: "mymap", Source: source}
	if _, err := staging.Stage(); err != nil {
		t.Fatalf("staging: %v", err)
	}
	if err := os.Remove(filepath.Join(source, "maps", "level.bsp")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "maps", "renamed.bsp"), "BSP")

	if _, err := staging.Stage(); err != nil {
		t.Fatalf("re-staging: %v", err)
	}
	if exists(filepath.Join(game, "mymap", "maps", "level.bsp")) {
		t.Error("the previous run's map is still there; the engine would have two to choose from")
	}
	if !exists(filepath.Join(game, "mymap", "maps", "renamed.bsp")) {
		t.Error("the new map was not staged")
	}
}

// Staging over the base game would overwrite what a user paid for, and the
// cleanup that followed would then remove it.
func TestStageRefusesTheGamesOwnDirectories(t *testing.T) {
	game, source := gameRoot(t), project(t)
	for _, name := range []string{"id1", "ID1", "hipnotic", "rogue"} {
		_, err := (engine.Staging{GameRoot: game, ModName: name, Source: source}).Stage()
		if err == nil {
			t.Fatalf("staging into %q was allowed", name)
		}
		if !exists(filepath.Join(game, "id1", "pak0.pak")) {
			t.Fatalf("staging into %q reached the base game", name)
		}
	}
}

func TestStageRefusesANameThatIsAPath(t *testing.T) {
	for _, name := range []string{"../elsewhere", "a/b", `a\b`, "", "."} {
		if err := engine.CheckModName(name); err == nil {
			t.Errorf("%q was accepted as a game directory name", name)
		}
	}
	if err := engine.CheckModName("ad_sepulcher"); err != nil {
		t.Errorf("an ordinary mod name was refused: %v", err)
	}
}

// A symbolic link in staged content is either a way out of the directory or a
// file that will not be there when the engine reads it.
func TestStageRefusesASymbolicLinkInTheSource(t *testing.T) {
	game, source := gameRoot(t), project(t)
	secret := filepath.Join(t.TempDir(), "secret")
	writeFile(t, secret, "not yours")
	if err := os.Symlink(secret, filepath.Join(source, "link")); err != nil {
		t.Skipf("this filesystem has no symbolic links: %v", err)
	}
	if _, err := (engine.Staging{GameRoot: game, ModName: "mymap", Source: source}).Stage(); err == nil {
		t.Fatal("a symbolic link was staged")
	}
}

func TestUnstageOnADirectoryNobodyStagedIsRefused(t *testing.T) {
	game := gameRoot(t)
	writeFile(t, filepath.Join(game, "mymap", "notes.txt"), "mine")
	if _, err := engine.Unstage(game, "mymap"); err == nil {
		t.Fatal("unstaging removed a directory with no staging record")
	}
	if !exists(filepath.Join(game, "mymap", "notes.txt")) {
		t.Error("the refusal removed something anyway")
	}
}
