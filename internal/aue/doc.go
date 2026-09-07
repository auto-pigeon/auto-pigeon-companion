// Package aue obtains a verified Auto-Pigeon Extractor and drives it as a
// subprocess.
//
// # What this replaces, and why the replacement is not an optimisation
//
// The extractor used to be EMBEDDED: the build script copied a platform's AUE
// binary into this repository, `//go:embed` compiled it into the Companion, and
// the first use wrote it to a temp directory and exec'd it from there. Three
// things were wrong with that, and they are different kinds of wrong.
//
//  1. **Licensing.** AUE is AGPL-3.0 and this repository is MIT. Embedding put
//     one program's bytes inside the other's artifact, so an MIT release
//     contained and appeared to cover an AGPL program — and a user holding the
//     Companion had no way to tell whose bytes they were running or where to
//     get their source.
//  2. **Verification.** Nothing checked the staged binary. `//go:embed`
//     resolves at compile time, so a stale or wrong-platform file left in that
//     directory shipped silently and failed on the user's machine; the build
//     script's own comment said so and asked the reader to be careful.
//  3. **Coupling.** A patched extractor needed a new Companion release, because
//     the only way to change the extractor was to rebuild the program it was
//     inside.
//
// So AUE is now DOWNLOADED, against a signed catalogue, at a version a signed
// compatibility manifest names for this Companion on this platform, and run as
// its own process. `internal/acquire` does the obtaining — the same verifier
// and the same cache every other managed tool uses, not a second updater — and
// this package decides WHICH version to ask for, checks that the executable
// speaks a contract this build can drive, and runs it.
//
// # There are exactly two ways to an executable
//
//	managed             a verified cache entry, at the version the manifest names
//	developer override  AUCOM_AUE_BINARY, unverified, local, and labelled so
//
// There is no third, and no fallback between them. A managed resolution that
// fails is an error the user reads; it never quietly becomes an override, and
// an override is never quietly treated as verified. [Provenance.Verified] is
// the one place that distinction is recorded, it travels with every runner, and
// every surface that shows an extractor shows it.
//
// # The protocol handshake happens before anything else
//
// A verified executable is not automatically one this build can talk to. The
// first thing a resolved runner does is ask it `protocol --json` and compare
// what it reports against the minimum the compatibility manifest declared: the
// majors must be equal and the minor at least the required one. A later major
// is a different contract, not a newer version of this one, and running it
// would mean parsing its output against a contract that has been replaced.
//
// The handshake is why the manifest carries `min_protocol` at all. Without it
// the Companion would be trusting a version NUMBER to imply a contract, which
// is exactly the assumption a rebuilt or forked extractor breaks.
//
// # Why a subprocess and not a library
//
// AUE's packages all live under internal/, and Go's internal-package rule
// blocks a different module from importing them. That is not an obstacle to
// route around: AUE's CLI is its supported public surface, its subcommands
// print JSON on stdout, and the separation is what keeps this repository MIT
// while AUE is AGPL. AUE never appears in go.mod.
package aue
