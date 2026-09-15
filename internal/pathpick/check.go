package pathpick

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Kind is what a caller is asking the user to choose.
type Kind string

const (
	// Directory is an existing directory: a game root, a project folder, a
	// cache. Most of what this program asks for.
	Directory Kind = "directory"
	// OpenFile is an existing file to read — a profile document, a `.map`.
	OpenFile Kind = "open-file"
	// SaveFile is a path to write. Its parent must exist; it need not.
	SaveFile Kind = "save-file"
)

// Kinds is every kind, in the order they are documented.
var Kinds = []Kind{Directory, OpenFile, SaveFile}

// Valid reports whether a string is a kind this package understands.
func (k Kind) Valid() bool {
	for _, candidate := range Kinds {
		if candidate == k {
			return true
		}
	}
	return false
}

// ErrCancelled is a dialog the user declined.
//
// Its own error because it is the one outcome that is not a problem: nothing is
// wrong, nothing needs reporting, and a caller that turned it into a message
// would be arguing with somebody who just pressed Cancel.
var ErrCancelled = errors.New("pathpick: the user cancelled the dialog")

// ErrNoHelper is a machine with no native file chooser this package can drive.
//
// The message names the fallback because that is what the reader has to do
// next, and because "no dialog is available" on its own reads like a defect
// rather than like a container with no desktop in it.
var ErrNoHelper = errors.New(
	"pathpick: this machine has no file chooser the Companion can open; type the path instead")

// Check validates a path a user supplied, from a dialog or from a text field.
//
// Both go through here, because a path that arrived from a subprocess's stdout
// is exactly as untrusted as one that arrived from a form field. What it
// guarantees to a caller is narrow and worth stating: the result is absolute,
// cleaned, free of control characters, and is on this machine the kind of thing
// that was asked for. It guarantees nothing about permissions — the operation
// that follows is what discovers those, and reports them far better than a
// pre-check could.
func Check(kind Kind, path string) (string, error) {
	if !kind.Valid() {
		return "", fmt.Errorf("pathpick: %q is not a kind of path (want %s)", kind, kindList())
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("no path was given")
	}
	// A NUL cannot be in a filename on any target, and a newline or a tab in
	// one is very nearly always a value that came from somewhere it should not
	// have — a helper that printed two lines, a paste that carried a line
	// ending. Refused rather than cleaned: silently trimming a control
	// character means accepting a path the user cannot see the whole of.
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%q contains a control character", path)
		}
	}

	expanded, err := expandHome(trimmed)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("%q is not an absolute path; "+
			"a relative one would depend on where the Companion happens to be running", trimmed)
	}
	cleaned := filepath.Clean(expanded)

	switch kind {
	case Directory:
		info, err := os.Stat(cleaned)
		if err != nil {
			return "", statError(cleaned, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("%s is a file; a directory was asked for", cleaned)
		}
	case OpenFile:
		info, err := os.Stat(cleaned)
		if err != nil {
			return "", statError(cleaned, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("%s is a directory; a file was asked for", cleaned)
		}
	case SaveFile:
		parent := filepath.Dir(cleaned)
		info, err := os.Stat(parent)
		if err != nil {
			return "", statError(parent, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("%s is not a directory, so nothing can be written inside it", parent)
		}
	}
	return cleaned, nil
}

// statError says why a path could not be looked at in words a person reads.
// Go's own `stat <path>: no such file or directory` repeats the path and names
// a system call; the page showed it verbatim (NEW_244D rehearsal). The cause
// stays wrapped for errors.Is.
func statError(path string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("there is no such file or folder: %s (%w)", path, fs.ErrNotExist)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("this account is not allowed to look at %s (%w)", path, fs.ErrPermission)
	}
	return fmt.Errorf("%s: %w", path, err)
}

// expandHome resolves a leading `~`.
//
// Not a compiled-in location: `~` is resolved against this machine's own home
// directory, the way a shell resolves it, and it exists because the alternative
// is a user typing thirty characters of their own path into a text field on a
// machine whose dialog helper is missing. Only a leading `~/` — or `~` alone —
// is expanded, so a directory genuinely called `~backup` is left as written.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving %q: this machine reports no home directory: %w", path, err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

func kindList() string {
	names := make([]string, 0, len(Kinds))
	for _, kind := range Kinds {
		names = append(names, string(kind))
	}
	return strings.Join(names, ", ")
}
