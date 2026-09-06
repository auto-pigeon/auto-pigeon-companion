// Package binding holds the machine-local half of a profile: where the
// executables actually are on this computer, which roots the user chose, what
// version was found when the tool was last asked, and what the user granted.
//
// # Why this is a separate package and not a member of a profile
//
// The rule is that a local binding can never be serialized into a shared
// profile. A rule like that is usually written in a comment and broken a year
// later by somebody adding a convenient field. Here it is the package graph:
//
//	internal/binding  ──imports──▶  internal/profile
//
// and nothing goes the other way. Go will not compile an import cycle, so no
// type in `internal/profile` can contain a [LocalBinding], a [Set] or anything
// else defined here — not as a member, not in a slice, not behind a pointer.
// The mistake is not caught by a reviewer or by a test; it is unrepresentable,
// and `TestProfilePackageDoesNotImportBinding` asserts the direction so that a
// future refactor cannot quietly reverse it.
//
// The content half of the same rule lives in `profile.CheckPortable`, which
// scans a document's strings for absolute paths, home directories, network
// locations and credentials. Between them: a binding cannot get into a document
// as a *type*, and its values cannot get in as *text*.
//
// # What is in here is the opposite of portable, on purpose
//
// Everything a profile is forbidden to contain is required here. Paths are
// absolute. The AUB Game Profile is identified by record id, because on this
// machine, against this account, that id is the precise answer. The grant
// records what one person decided. None of it means anything anywhere else, and
// none of it is ever published.
package binding
