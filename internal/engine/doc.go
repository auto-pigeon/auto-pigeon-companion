// Package engine is the part of launching a game that is about *this machine*:
// where the game's data is, whether the engine the binding names is still
// there, and how content you built gets somewhere the engine will look for it.
//
// # What is deliberately not here
//
// Not the command line. What arguments an engine takes is an engine profile's
// business, and a Go function that knew Ironwail's flags would be the first of
// six, then the first of sixty. Nothing in this package branches on which
// engine it is; everything it needs, it reads out of the document.
//
// Not the process. Starting, supervising, stopping and recording a game is
// [github.com/andrea-dintino/auto-pigeon-companion/internal/job]'s, the same as
// a compile, because a second execution path is a second set of rules about
// what a running program may do.
//
// # Discovery proposes, it never decides
//
// [Scanner.Detect] looks in the places Steam and GOG put things and reports
// what it found. It reads directory entries and nothing else: it opens no game
// file, copies nothing, and uploads nothing. Commercial game data is the user's,
// it is not redistributable, and a program that "helpfully" copied
// `id1/pak0.pak` somewhere would be doing the one thing this repository must
// never do.
//
// A candidate is a *proposal*. Nothing here writes a binding, and the caller
// that does has to have been given a path by a person — see the CLI's
// `companion engine bind`, which will not take a detected path unless it is
// named on the command line.
//
// # Staging is reversible, and cleanup can only remove what it put there
//
// A Quake engine loads content out of a directory beside `id1`. A project lives
// somewhere else entirely. [Staging] copies the built content into a game
// directory and writes down every file it wrote, with its digest; [Unstage]
// removes exactly those files and stops at anything that has changed since. A
// cleanup implemented as "delete the mod directory" would be a cleanup that one
// day deletes a directory somebody had been keeping their own work in.
package engine
