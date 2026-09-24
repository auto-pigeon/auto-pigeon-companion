// Package feedback builds the compatibility report a user may choose to send
// when a work-in-progress game family does not do what they expected.
//
// # The report is constructed, not collected
//
// The obvious way to write this would be to gather what is on the machine — the
// job's log, the map that failed, the paths involved — and then remove the
// parts that must not leave. `AUP/AUCOM 215` says the opposite: *"Do not
// auto-upload maps, logs or paths."* So nothing is gathered and then filtered.
// [Report] has no member that can hold a file, a log or a location, and
// [Build] assembles one out of typed facts the caller already had.
//
// That difference matters because a filter is only as good as the last person
// who thought about it. A struct with no field for a path cannot leak one
// however carelessly a future surface calls it.
//
// # Diagnostics are the Companion's own words
//
// A [Diagnostic] carries the id and severity of the rule that fired and *the
// message the profile document declares for it* — text this program shipped,
// which [github.com/auto-pigeon/auto-pigeon-companion/internal/profile.CheckPortable]
// already refuses to let contain a path or a credential. The line the tool
// actually printed — which contains the user's filenames, and on a bad day a
// token a tool found by other means — is never carried.
//
// This is not a reduction in usefulness. "`texture_missing` fired 14 times on a
// Quake II compile with ericw-tools 2.0.0-alpha7" is the report a maintainer
// can act on. The names of the fourteen textures in somebody's unreleased map
// are not.
//
// # Consent is four booleans, and they default to none
//
// [Consent] names the four things that may be attached: versions, profiles, the
// operation, and the diagnostics. Every one starts false, [Build] drops what is
// not consented to, and the report records what was chosen — so a reader can
// tell "the user did not share diagnostics" from "the user shared diagnostics
// and none fired", which is a distinction a bare absence destroys.
//
// Maps, logs and filesystem paths are not on that list at any setting. There is
// no flag that adds them, because there is nowhere for them to go.
//
// # Nothing here sends anything
//
// [Build] returns a document and [Encode] renders it. Handing it to somebody is
// the user's act, performed by a surface that shows them the finished text
// first. A package that could post it would be a package that could post it by
// accident.
package feedback
