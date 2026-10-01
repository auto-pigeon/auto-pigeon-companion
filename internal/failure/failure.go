// Package failure names the kinds of thing that stop a build, as tokens.
//
// # Why a class and not only a sentence
//
// Every failure here already had a sentence, and the sentences were good. What
// they could not do is be told apart by a program: "the map leaks", "Q3Map2 is
// not installed", "this archive is damaged" and "no base game is bound" were
// four strings, and a page that wanted to say "install the compiler" for one
// and "open the leak file" for another had to match on wording. `Q3_010` asks
// for those to be distinguished, and a class is the smallest thing that does
// it: a token carried beside the message, never instead of it.
//
// A class is attached where the failure is KNOWN — the function that refused
// the archive knows it is an archive — and read back with [Of]. Nothing here
// infers a class from an error's text.
package failure

import "errors"

// The classes. A token is lower-case with underscores, the same shape a
// profile's own diagnostic `class` has, because a build reports both in one
// place.
const (
	// ToolUnavailable: the program a step needs is not on this machine, or
	// what is there cannot be started.
	ToolUnavailable = "tool_unavailable"
	// PlatformUnsupported: the profile declares it does not run here.
	PlatformUnsupported = "platform_unsupported"
	// GameDataMissing: a game data root the build needs is not bound, or holds
	// no game directory. Never the Companion's to supply.
	GameDataMissing = "game_data_missing"
	// ArchiveDamaged: a PK3 in an approved root cannot be read as one.
	ArchiveDamaged = "archive_damaged"
	// ContentRefused: an approved root holds something that may not be staged
	// — a link out of the root, more files than the bound.
	ContentRefused = "content_refused"
	// FSGameInvalid: the mod directory name is not a name.
	FSGameInvalid = "fs_game_invalid"
	// FSGameNotFound: no approved root has a directory of that name.
	FSGameNotFound = "fs_game_not_found"
	// InputInvalid: a file handed to a stage is not the kind of file its role
	// says it is.
	InputInvalid = "input_invalid"
	// OutputMissing: a stage exited and a required output is not there.
	OutputMissing = "output_missing"
	// OutputInvalid: a stage wrote a file that is not what its role says.
	OutputInvalid = "output_invalid"
	// ToolFailed: the program exited with a failure status.
	ToolFailed = "tool_failed"
	// TimedOut: the program was stopped at its time bound.
	TimedOut = "timed_out"
	// Cancelled: somebody stopped it.
	Cancelled = "cancelled"
	// ConverterUnavailable: an APMap input and no extractor to convert it.
	ConverterUnavailable = "converter_unavailable"
	// ConversionRefused: the extractor would not write a `.map` for the input.
	ConversionRefused = "conversion_refused"
	// FatalDiagnostic: a profile's `fatal` rule matched and named no class.
	FatalDiagnostic = "fatal_diagnostic"
	// PackageHeld: a Quake III map package was not written because something
	// an engine needs is missing, ungranted or refused.
	PackageHeld = "package_held"
	// InstallConflict: a package's archive could not be installed because a
	// file of its name is already there and is not the one this program put.
	InstallConflict = "install_conflict"
	// LoadOrderShadowed: the package was not installed because another archive
	// or a loose file in the same game directory would be loaded INSTEAD of
	// its map.
	LoadOrderShadowed = "load_order_shadowed"
	// MapNotLoaded: the engine started and did not accept the map — it said it
	// could not find it, or stopped with an error before loading it.
	MapNotLoaded = "map_not_loaded"
	// EngineStopped: the engine process ended before it reported loading the
	// map and printed no line that says why.
	EngineStopped = "engine_stopped"
)

// Error is an error with a class.
type Error struct {
	Class string
	Err   error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// As attaches a class to an error. A nil error stays nil, and an error that
// already has a class keeps it: the function nearest the cause knew best.
func As(class string, err error) error {
	if err == nil {
		return nil
	}
	if Of(err) != "" {
		return err
	}
	return &Error{Class: class, Err: err}
}

// Of is the class an error carries, or "" when it carries none.
func Of(err error) string {
	var classified *Error
	if errors.As(err, &classified) {
		return classified.Class
	}
	return ""
}
