// Package enginefixture is a stand-in Quake engine: a program that records the
// argv, the environment and the working directory it was started with, then
// behaves the way an engine behaves — becoming ready, crashing, or staying up
// until it is stopped.
//
// # Why acceptance needs one
//
// Every claim this repository makes about launching a game is a claim about a
// command line: that `-basedir` gets the game root and not the mod root, that a
// path with a space in it arrives as one argument, that a dedicated server is
// started with different arguments from a client. None of that can be checked
// by starting a real engine, for three separate reasons:
//
//   - Quake's engines need `id1/pak0.pak`, which is commercial game data. A
//     test suite that needed it would be a test suite nobody outside one
//     machine could run, and shipping the data to fix that is exactly what the
//     Companion must never do.
//   - The engines are not on the machine. Six upstream projects, six
//     platforms, and a CI runner that has none of them.
//   - A real engine opens a window and waits for a person. What a test needs is
//     a program that says what it was asked to do and then stops.
//
// So the fixture is the *measuring instrument*: it proves the Companion built
// the argv it said it built. It proves nothing about whether Ironwail accepts
// that argv — that is what the curated profiles' `unverified` platform status
// says out loud, and no fixture can upgrade it.
//
// # Why it is a normal package and not a test helper
//
// The other fixtures in this repository live in one package's `helper_test.go`
// and re-execute the test binary. That is right when one package needs them.
// This one is needed by `internal/engine`, by `internal/job` and by the CLI
// tests, and three copies of a program that has to agree about its own
// recording format is three chances for them to stop agreeing.
//
// Nothing outside a test imports it, so it is not reachable from the shipped
// command: [Main] runs only when a test binary's TestMain dispatches on [Flag].
package enginefixture
