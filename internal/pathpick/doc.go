// Package pathpick is how a browser-based GUI asks for a path on the user's
// disk without the browser ever being given the disk.
//
// # The problem this exists for
//
// The Companion's window is the user's own browser (see internal/web), and a
// page cannot open a file dialog that returns a *path*. `<input type=file>`
// hands JavaScript the file's bytes and a bare name; the directory a build
// writes into, the game root an engine reads, the `id1` beside it — none of
// those are files a page can be handed at all. The alternative most programs
// reach for is a GUI toolkit, and that is exactly the dependency this
// repository refuses: it would put cgo, a widget tree and six cross-compilation
// problems into a binary whose whole shape is `go build` with no C.
//
// So the dialog is opened by the Companion process, in the desktop's own file
// chooser, and the ONE path the user picked comes back. Nothing else crosses:
// the page never enumerates a directory, never reads a byte it was not handed,
// and cannot ask for a path the user did not choose in a dialog they saw.
//
// # There is no helper on every machine, and that is a normal outcome
//
// A headless server, a minimal container, a Linux desktop with neither GNOME
// nor KDE's dialog binary installed: [ErrNoHelper] is what those report, and it
// is not a failure. The caller falls back to a typed path, which goes through
// [Check] — the same validation the dialog's answer goes through, because a
// path is untrusted whichever of the two produced it.
//
// A helper that is present and is DECLINED is a different outcome again:
// [Result.Cancelled] means the user said no, and a caller that treated that as
// an error would be a caller that argues with them.
//
// # Every helper is data, and the platform is a field
//
// The adapters are a table keyed by GOOS, and [Picker] carries its own `GOOS`,
// its own lookup and its own runner. That is what lets one test on Linux check
// the argv this package builds for PowerShell, and check what it does when the
// helper is missing, when it is cancelled, and when it prints something absurd
// — none of which can be checked by a build-tagged file that only compiles on
// the platform it describes.
//
// # What comes back is not trusted
//
// A helper is a subprocess and its output is parsed, not believed. The first
// line only, trimmed, and then through [Check]: absolute, no control
// characters, and of the kind that was asked for. A dialog that was cancelled
// prints nothing; a dialog that was killed prints nothing; and a value that
// arrived from neither is refused rather than returned as a path.
package pathpick
