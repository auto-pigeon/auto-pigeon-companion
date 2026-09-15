package pathpick

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The typed-path fallback is what a machine with no dialog helper uses, so it
// carries the same weight as the dialog and gets the same scrutiny.
func TestCheck(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "pak0.pak")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, testCase := range []struct {
		name    string
		kind    Kind
		path    string
		want    string
		wantErr string
	}{
		{name: "an existing directory", kind: Directory, path: dir, want: dir},
		{name: "an existing file", kind: OpenFile, path: file, want: file},
		{name: "a file to write beside one that exists", kind: SaveFile,
			path: filepath.Join(dir, "out.pk3"), want: filepath.Join(dir, "out.pk3")},
		{name: "a path with a redundant segment", kind: Directory,
			path: dir + "/./", want: dir},
		{name: "nothing", kind: Directory, path: "   ", wantErr: "no path was given"},
		{name: "a relative path", kind: Directory, path: "quake", wantErr: "not an absolute path"},
		{name: "a path with a newline in it", kind: Directory, path: dir + "\nrm -rf /",
			wantErr: "control character"},
		{name: "a directory that is not there", kind: Directory,
			path: filepath.Join(dir, "absent"), wantErr: "no such file"},
		{name: "a file where a directory belongs", kind: Directory, path: file,
			wantErr: "a directory was asked for"},
		{name: "a directory where a file belongs", kind: OpenFile, path: dir,
			wantErr: "a file was asked for"},
		{name: "somewhere to write inside a directory that is not there", kind: SaveFile,
			path: filepath.Join(dir, "absent", "out.pk3"), wantErr: "no such file"},
		{name: "a kind nobody offers", kind: Kind("anything"), path: dir,
			wantErr: "is not a kind of path"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := Check(testCase.kind, testCase.path)
			if testCase.wantErr != "" {
				if err == nil {
					t.Fatalf("Check(%q, %q) = %q, want an error mentioning %q",
						testCase.kind, testCase.path, got, testCase.wantErr)
				}
				if !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("Check error = %v, want it to mention %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Check(%q, %q): %v", testCase.kind, testCase.path, err)
			}
			if got != testCase.want {
				t.Fatalf("Check = %q, want %q", got, testCase.want)
			}
		})
	}
}

// `~` is resolved against this machine's home directory, the way a shell does
// it — and only when it is the whole path or the first segment, so a directory
// somebody actually called `~backup` survives.
func TestCheckExpandsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("this machine reports no home directory: %v", err)
	}
	got, err := Check(Directory, "~")
	if err != nil {
		t.Fatalf("Check(directory, ~): %v", err)
	}
	if got != filepath.Clean(home) {
		t.Fatalf("Check(directory, ~) = %q, want %q", got, home)
	}

	if _, err := Check(Directory, "~backup"); err == nil ||
		!strings.Contains(err.Error(), "not an absolute path") {
		t.Fatalf("Check(directory, ~backup) = %v, want it left alone and refused as relative", err)
	}
}

// TestAMissingPathIsSaidInWords: the page showed Go's `stat <path>: no such
// file or directory` verbatim (NEW_244D). The sentence names the path once,
// without a system call, and still answers errors.Is.
func TestAMissingPathIsSaidInWords(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "gone.map")
	_, err := Check(OpenFile, absent)
	if err == nil {
		t.Fatal("a missing file was accepted")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(ErrNotExist) = false for %v", err)
	}
	if strings.Contains(err.Error(), "stat ") || strings.Count(err.Error(), absent) != 1 {
		t.Errorf("message = %q", err.Error())
	}
}
