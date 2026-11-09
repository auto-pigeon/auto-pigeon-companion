//go:build windows

package main

// `NEW_440` on a real Windows: canonical_wad_test.go's windowsOpens is a model
// of how ericw qbsp's WAD lookup resolves on Windows; here Windows itself
// answers (`GetFullPathNameW`, through syscall.FullPath, and a real open). The
// build workflow runs this on its windows-latest runner; nothing on Linux or
// macOS compiles it, so a pass here is only ever a native observation.
//
// qbsp's last fallback (`LoadTexturePath`, `qbsp/map.cc`) opens the entry as it
// is, relative to its working folder, so the working folder stands in for the
// WAD folder: an entry Windows resolves outside it reaches a file the build
// does not own.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// fullPath is what Windows opens for name from the current folder.
func fullPath(t *testing.T, name string) string {
	t.Helper()
	full, err := syscall.FullPath(name)
	if err != nil {
		t.Fatalf("GetFullPathName(%q): %v", name, err)
	}
	return full
}

func TestWindowsOpensTheCopysWADNamesInsideTheFolder(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	folder := fullPath(t, ".")
	volume := strings.ToUpper(filepath.VolumeName(folder))
	other := "Q:"
	if volume == other {
		other = "R:"
	}
	inside := func(full string) bool { return strings.HasPrefix(strings.ToLower(full), strings.ToLower(folder)+`\`) }

	// The vector on this Windows: each raw entry resolves outside the folder.
	raw := []string{other + "secret.wad", `\root.wad`, `\\server\share\net.wad`, `..\..\up.wad`, "/abs/x.wad", other + `\quake\id1\gfx.wad`}
	for _, entry := range raw {
		if full := fullPath(t, entry); inside(full) {
			t.Errorf("Windows resolves the raw entry %q inside the folder (%q): it is not the vector here", entry, full)
		} else {
			t.Logf("RAW %q -> %q (outside %q)", entry, full, folder)
		}
	}
	// The model agrees with Windows where both resolve from the folder.
	for _, entry := range []string{`..\..\up.wad`, "x.wad.", "x.wad .", `sub\.\y.wad`, `sub\..\z.wad`} {
		if model, native := windowsOpens(folder, entry), fullPath(t, entry); !strings.EqualFold(model, native) {
			t.Errorf("the model says %q for %q, Windows %q", model, entry, native)
		}
	}
	// What this Windows does with names the boundary refuses whatever the
	// answer is (device names changed in Windows 11; `.. ` is not `..` in the
	// documented rules). Recorded, not asserted.
	for _, entry := range []string{"NUL.wad", "con.wad", "COM1.wad", "nul.tar.wad", `.. \x.wad`, "x.wad:stream"} {
		t.Logf("REFUSED-ANYWAY %q -> %q", entry, fullPath(t, entry))
	}

	// The copy: every name opens as itself, directly in the folder, and the
	// file a bare-name open reaches is the file written there.
	entries := append(append([]string(nil), raw...), "C:secret.wad", "metal.wad", "my textures.wad", "q1-base_2.Wad", `.. \.. \dots.wad`)
	out, changed, err := canonicalMapText([]byte(world(`"wad" "`+strings.Join(entries, ";")+`"`+"\n", box("sand"))))
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	t.Logf("reduced %q", changed)
	for _, name := range strings.Split(wadOf(t, out), ";") {
		full := fullPath(t, name)
		if !strings.EqualFold(full, folder+`\`+name) || filepath.Base(full) != name {
			t.Fatalf("Windows opens %q for the copy's name %q, not %q", full, name, folder+`\`+name)
		}
		if err := os.WriteFile(filepath.Join(folder, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		opened, err := os.Stat(name)
		if err != nil {
			t.Fatalf("the bare name %q does not open: %v", name, err)
		}
		written, err := os.Stat(filepath.Join(folder, name))
		if err != nil || !os.SameFile(opened, written) {
			t.Fatalf("the bare name %q opens another file than the one in the folder", name)
		}
		t.Logf("COPY %q -> %q", name, full)
	}
}
