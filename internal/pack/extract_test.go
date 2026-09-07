package pack

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractRoundTrips(t *testing.T) {
	for _, id := range []string{"quake-pak", "quake3-pk3"} {
		t.Run(id, func(t *testing.T) {
			target := mustTarget(t, id)
			dir := t.TempDir()
			path, _ := writeArchive(t, dir, "x"+target.Extension(), []Member{
				memberOf("maps/e1m1.bsp", "the compiled level"),
				memberOf("gfx/palette.lmp", "palette"),
				memberOf("sound/empty.wav", ""),
			}, target)

			dest := filepath.Join(t.TempDir(), "out")
			result, err := Extract(path, target.Format, ExtractOptions{Dest: dest})
			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if len(result.Files) != 3 {
				t.Fatalf("extracted %d files, want 3", len(result.Files))
			}
			content, err := os.ReadFile(filepath.Join(dest, "maps", "e1m1.bsp"))
			if err != nil {
				t.Fatalf("reading the extracted file: %v", err)
			}
			if string(content) != "the compiled level" {
				t.Fatalf("extracted %q", content)
			}
			for _, entry := range result.Files {
				if entry.SHA256 == "" {
					t.Fatalf("%s was extracted with no digest recorded", entry.Path)
				}
			}
		})
	}
}

func TestExtractRefusesToEscapeThroughAPreexistingSymlink(t *testing.T) {
	// The archive's names are all innocent. The destination is what is
	// hostile: `maps` is already a link to somewhere else, and a naive
	// extractor writes through it.
	outside := t.TempDir()
	dest := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("creating the destination: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dest, "maps")); err != nil {
		t.Skipf("this filesystem does not do symbolic links: %v", err)
	}

	dir := t.TempDir()
	target := mustTarget(t, "quake-pak")
	path, _ := writeArchive(t, dir, "x.pak", []Member{memberOf("maps/e1m1.bsp", "level")}, target)

	_, err := Extract(path, FormatPAK, ExtractOptions{Dest: dest})
	if err == nil {
		// If it did write, it must at least not have written outside.
		if _, statErr := os.Stat(filepath.Join(outside, "e1m1.bsp")); statErr == nil {
			t.Fatal("a member was written outside the destination through a pre-existing symbolic link")
		}
		t.Fatal("extraction through a symlinked subdirectory was allowed")
	}
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("error is %v, want an ErrUnsafePath", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "e1m1.bsp")); statErr == nil {
		t.Fatal("a member was written outside the destination")
	}
}

func TestExtractRefusesAnArchiveWithATraversalName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "evil.pak")
	raw := buildPAKBytes(t, []pakFile{{Name: "../../escaped.txt", Data: []byte("x")}}, nil)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if _, err := Extract(path, FormatPAK, ExtractOptions{Dest: dest}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("error is %v, want an ErrUnsafePath", err)
	}
}

func TestExtractRefusesACaseCollision(t *testing.T) {
	// Two members that are one file on Windows and macOS. Extracting would
	// silently write one over the other; refusing says so.
	dir := t.TempDir()
	path := filepath.Join(dir, "collide.pk3")
	writeRawZip(t, path, func(w *zip.Writer) {
		for _, name := range []string{"maps/E1M1.bsp", "maps/e1m1.bsp"} {
			part, err := w.Create(name)
			if err != nil {
				t.Fatalf("creating %q: %v", name, err)
			}
			part.Write([]byte(name))
		}
	})
	dest := filepath.Join(t.TempDir(), "out")
	_, err := Extract(path, FormatPK3, ExtractOptions{Dest: dest})
	if err == nil {
		t.Fatal("a case-colliding archive was extracted")
	}
	if !strings.Contains(err.Error(), "capitalisation") {
		t.Fatalf("error is %v", err)
	}
}

func TestExtractDoesNotOverwriteWithoutBeingTold(t *testing.T) {
	dir := t.TempDir()
	target := mustTarget(t, "quake-pak")
	path, _ := writeArchive(t, dir, "x.pak", []Member{
		memberOf("maps/e1m1.bsp", "from the archive"),
		memberOf("gfx/palette.lmp", "palette"),
	}, target)

	dest := filepath.Join(t.TempDir(), "out")
	writeFile(t, dest, "maps/e1m1.bsp", "the file that was already there")

	_, err := Extract(path, FormatPAK, ExtractOptions{Dest: dest})
	if err == nil {
		t.Fatal("an existing file was overwritten")
	}
	if !errors.Is(err, ErrWouldOverwrite) {
		t.Fatalf("error is %v, want an ErrWouldOverwrite", err)
	}
	// The refusal happens before anything is written, so the second member is
	// not on disk either.
	if _, statErr := os.Stat(filepath.Join(dest, "gfx", "palette.lmp")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("the refusal came after some members had already been written")
	}
	kept, err := os.ReadFile(filepath.Join(dest, "maps", "e1m1.bsp"))
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if string(kept) != "the file that was already there" {
		t.Fatalf("the existing file is now %q", kept)
	}

	// And with --replace it goes through.
	if _, err := Extract(path, FormatPAK, ExtractOptions{Dest: dest, Replace: true}); err != nil {
		t.Fatalf("--replace was refused: %v", err)
	}
	replaced, err := os.ReadFile(filepath.Join(dest, "maps", "e1m1.bsp"))
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if string(replaced) != "from the archive" {
		t.Fatalf("after --replace the file is %q", replaced)
	}
}

func TestExtractStopsAtTheBudget(t *testing.T) {
	dir := t.TempDir()
	target := mustTarget(t, "quake-pak")
	path, _ := writeArchive(t, dir, "x.pak", []Member{
		memberOf("a.dat", strings.Repeat("a", 4096)),
		memberOf("b.dat", strings.Repeat("b", 4096)),
	}, target)

	dest := filepath.Join(t.TempDir(), "out")
	_, err := Extract(path, FormatPAK, ExtractOptions{Dest: dest, Budget: Budget{MaxTotalSize: 5000}})
	if err == nil {
		t.Fatal("8192 bytes were extracted against a 5000-byte budget")
	}
	if !errors.Is(err, ErrArchiveBomb) {
		t.Fatalf("error is %v, want an ErrArchiveBomb", err)
	}
}

func TestExtractOnlyTheNamedMembers(t *testing.T) {
	dir := t.TempDir()
	target := mustTarget(t, "quake3-pk3")
	path, _ := writeArchive(t, dir, "x.pk3", []Member{
		memberOf("maps/e1m1.bsp", "level"),
		memberOf("gfx/palette.lmp", "palette"),
	}, target)

	dest := filepath.Join(t.TempDir(), "out")
	result, err := Extract(path, FormatPK3, ExtractOptions{Dest: dest, Only: []string{"maps/e1m1.bsp"}})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(result.Files) != 1 || result.Skipped != 1 {
		t.Fatalf("extracted %d files, skipped %d", len(result.Files), result.Skipped)
	}
	if _, err := os.Stat(filepath.Join(dest, "gfx", "palette.lmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a member nobody asked for was extracted")
	}
	if _, err := Extract(path, FormatPK3, ExtractOptions{Dest: dest, Only: []string{"nothing.txt"}}); err == nil {
		t.Fatal("a member that is not in the archive was silently ignored")
	}
}

func TestExtractRefusesAMemberThatDoesNotMatchItsDeclaration(t *testing.T) {
	// The declared size is what the destination was sized against, so a member
	// that produces something else is stopped and its half-written file is
	// removed.
	dir := t.TempDir()
	path := filepath.Join(dir, "liar.pk3")
	writeRawZip(t, path, func(w *zip.Writer) {
		part, err := w.Create("a.dat")
		if err != nil {
			t.Fatalf("creating: %v", err)
		}
		part.Write([]byte(strings.Repeat("a", 100)))
	})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	patched := filepath.Join(dir, "patched.pk3")
	if err := os.WriteFile(patched, patchCentralUncompressedSize(t, raw, 400), 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if _, err := Extract(patched, FormatPK3, ExtractOptions{Dest: dest}); err == nil {
		t.Fatal("a member that lied about its size was extracted")
	}
	if _, err := os.Stat(filepath.Join(dest, "a.dat")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a half-written member was left behind")
	}
}

func TestExtractedFilesAreNotExecutable(t *testing.T) {
	// A PK3 can carry a Unix mode. Nothing in a game archive should arrive
	// executable because the packer's file happened to be.
	dir := t.TempDir()
	path := filepath.Join(dir, "modes.pk3")
	writeRawZip(t, path, func(w *zip.Writer) {
		header := &zip.FileHeader{Name: "run.sh", Method: zip.Store}
		header.CreatorVersion = 3 << 8
		header.SetMode(0o777)
		part, err := w.CreateHeader(header)
		if err != nil {
			t.Fatalf("creating: %v", err)
		}
		part.Write([]byte("#!/bin/sh\n"))
	})
	dest := filepath.Join(t.TempDir(), "out")
	if _, err := Extract(path, FormatPK3, ExtractOptions{Dest: dest}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	info, err := os.Stat(filepath.Join(dest, "run.sh"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode()&0o111 != 0 {
		t.Fatalf("extracted with mode %s; the archive's permission bits were honoured", info.Mode())
	}
}

func TestInspectRefusesADirectoryAndAMissingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Inspect(dir, FormatPAK, Budget{}); err == nil {
		t.Fatal("a directory was inspected as an archive")
	}
	if _, err := Inspect(filepath.Join(dir, "nope.pak"), FormatPAK, Budget{}); err == nil {
		t.Fatal("a missing file was inspected")
	}
}

func TestInspectGuessesTheFormatFromTheName(t *testing.T) {
	dir := t.TempDir()
	path, _ := writeArchive(t, dir, "guess.pk3", []Member{memberOf("a.txt", "a")}, mustTarget(t, "quake3-pk3"))
	inspection, err := Inspect(path, "", Budget{})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if inspection.Format != FormatPK3 {
		t.Fatalf("guessed %q", inspection.Format)
	}
	// A wrong guess reports the format's own refusal rather than a guess about
	// the guess.
	renamed := filepath.Join(dir, "guess.pak")
	if err := os.Rename(path, renamed); err != nil {
		t.Fatalf("renaming: %v", err)
	}
	_, err = Inspect(renamed, "", Budget{})
	if err == nil {
		t.Fatal("a PK3 named .pak was read as a PAK")
	}
	if !strings.Contains(err.Error(), "PACK") {
		t.Fatalf("error is %v, want PAK's own refusal", err)
	}
	// And with no recognisable extension it asks rather than guesses.
	unnamed := filepath.Join(dir, "guess.bin")
	if err := os.Rename(renamed, unnamed); err != nil {
		t.Fatalf("renaming: %v", err)
	}
	if _, err := Inspect(unnamed, "", Budget{}); err == nil || !strings.Contains(err.Error(), "say which format") {
		t.Fatalf("error is %v", err)
	}
}

// A hard link is not a kind of file, it is a second name for one, and Lstat
// cannot tell you that a destination has another name. So a replacing
// extraction that opened the destination with O_TRUNC would write the archive's
// bytes into whatever else that name points at.
//
// The replacing path therefore writes a temporary file and renames it, which
// replaces the directory entry and cannot write through anything.
func TestReplacingAnExtractedFileDoesNotWriteThroughAHardLink(t *testing.T) {
	target := mustTarget(t, "quake3-pk3")
	dir := t.TempDir()
	archive, _ := writeArchive(t, dir, "content"+target.Extension(), []Member{
		memberOf("sound/x.wav", "archive bytes"),
	}, target)

	dest := filepath.Join(t.TempDir(), "out")
	// Somebody's file, and a second name for it inside the extraction target.
	secret := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dest, "sound"), 0o755); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(dest, "sound", "x.wav")
	if err := os.Link(secret, planted); err != nil {
		t.Skipf("this filesystem does not do hard links: %v", err)
	}

	if _, err := Extract(archive, target.Format, ExtractOptions{Dest: dest, Replace: true}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if body, err := os.ReadFile(secret); err != nil || string(body) != "PRIVATE KEY" {
		t.Errorf("the linked file was written through: %q, %v", body, err)
	}
	if body, err := os.ReadFile(planted); err != nil || string(body) != "archive bytes" {
		t.Errorf("the extracted member is %q, %v", body, err)
	}
}

// The replacing path renames into place, so an extraction that fails partway
// leaves the file that was there rather than half of the new one.
func TestAFailedReplacementLeavesTheOriginalFileIntact(t *testing.T) {
	target := mustTarget(t, "quake3-pk3")
	dir := t.TempDir()
	archive, _ := writeArchive(t, dir, "content"+target.Extension(), []Member{
		memberOf("a.txt", "new a"),
		memberOf("b.txt", strings.Repeat("b", 4096)),
	}, target)

	dest := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dest, name), []byte("original "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A budget that runs out partway through the second member.
	_, err := Extract(archive, target.Format, ExtractOptions{
		Dest: dest, Replace: true, Budget: Budget{MaxTotalSize: 100, MaxEntries: 10, MaxEntrySize: 100},
	})
	if err == nil {
		t.Fatal("an extraction past its budget was not refused")
	}
	body, readErr := os.ReadFile(filepath.Join(dest, "b.txt"))
	if readErr != nil {
		t.Fatalf("the original file is gone: %v", readErr)
	}
	if string(body) != "original b.txt" {
		t.Errorf("the original file was replaced by a failed extraction: %q", body)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".extracting-") {
			t.Errorf("a staging file was left behind: %s", entry.Name())
		}
	}
}
