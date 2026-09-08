// Package nativeacceptance is the operator's own acceptance run: one command,
// on the machine the artifact is for, producing a result bundle somebody else
// can merge.
//
// # What it corrects
//
// `AUT/AUCOM 219` asked one Linux-amd64 agent to execute Windows, Linux-arm64
// and macOS targets and to launch Quake without owned game data. Neither is
// something an agent on one machine can do, and neither failure would have been
// a product defect. Cross-compilation proves that an artifact BUILDS. It proves
// nothing about whether that artifact starts, registers a URI scheme, finds a
// compiler, writes a PAK or composes an engine command on the machine it was
// built for. Those are native observations, and the only honest way to have one
// is for something to run natively and say so.
//
// So this package makes the evidence model honest in two directions at once. It
// gives the project owner a command they can run on a Windows amd64 or Linux
// arm64 machine and hand back a small document; and it gives that document a
// shape that CANNOT be mistaken for a claim about a platform nobody ran it on.
//
// # The bundle is constructed, not collected
//
// `internal/feedback` established this and the reason is the same one here: a
// filter is only as good as the last person who thought about it, and a struct
// with no field for a path cannot leak one however carelessly a future lane is
// written. [Bundle] has no member a byte of game data, a log, a credential or a
// user name could travel in. [Observation.Detail] is the one free-text member,
// and everything that reaches it goes through [Redactor] — and then
// [Bundle.Validate] refuses a document in which anything that still looks like
// an absolute path survived.
//
// That last part is `AUT/AUCOM 230`'s arrangement one repository over: an
// allow-list constructs the document and a scan is defence in depth over it.
// Neither replaces the other. The construction is what makes a field nobody
// approved impossible; the scan is what catches a field somebody approved and
// then filled carelessly.
//
// # Every lane drives the SHIPPED program, as a child process
//
// Not an in-process call. `AUT/AUCOM 219` found three defects that exist only
// at that boundary — a success message naming a subcommand that does not
// exist, four commands printing usage with flags in an order their own parser
// rejects, a preview reporting on a selection the writer refuses — and not one
// of them is reachable from a function call. What an operator runs is a process
// with an argv and an exit status, so that is what is measured.
//
// # The run is isolated, and that is not a convenience
//
// Every lane runs against a HOME this run made. The purge lane deletes
// configuration, granted profiles, the catalogue's revocation ratchet and
// licence acknowledgements; an acceptance run that did that to the operator's
// own machine would cost them the decisions they had made, which is a high
// price for a check. An isolated home is also the only way to observe a
// PORTABLE FIRST START at all: a second run on a machine that already has a
// configured Companion would be observing the second run.
//
// The two things this deliberately does not isolate are the ones no environment
// variable moves: a Windows HKCU registration and a macOS bundle. Those are
// reported as `not_applicable` with the reason, rather than performed.
//
// # Three answers, not two
//
// A lane that could not run is [NotAvailable] or [NotApplicable], never a pass.
// `auto-pigeon-tools/AGENTS.md` §3e's rule, and the reason the merge on the
// development machine can tell `build_only` from `native_pass`: a bundle full
// of not-available lanes is a bundle that proves the artifact starts, and the
// matrix says exactly that much.
package nativeacceptance
