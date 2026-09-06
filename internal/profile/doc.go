// Package profile is Auto-Pigeon Companion's extension model: the versioned,
// portable documents that describe an external tool, a game engine, or a
// pipeline built from them.
//
// # Why this package exists before any tool is wired up
//
// The Companion drives programs it does not own — map compilers, engines — and
// every one of them is a separate process under its own licence. The tempting
// shape is a Go switch: `if tool == "qbsp"` here, `if engine == "ironwail"`
// there. That shape has two costs that are only visible later. It makes a
// third tool a code change rather than a document, and it makes the built-in
// tools a privileged path that no user-authored equivalent can reach, so the
// extension model is never actually exercised by the people who wrote it.
//
// So the rule this package exists to enforce is: **there is one execution
// model, and the built-in tools go through it.** A curated EricW profile and a
// profile a user typed by hand differ in trust — who vouches for it, what the
// user had to agree to — and in nothing else. Resolve produces the same kind of
// [Invocation] for both, and there is no branch anywhere that asks whether a
// profile is built in before deciding what to run.
//
// # Profiles are data, and specifically not programs
//
// A profile can name an executable and give it an argument array. It cannot
// contain a shell string, a script, a hook, an installer, a regular expression,
// an embedded binary, or a library to load. This is not caution for its own
// sake: a profile is a file that arrives from other people, and the whole
// design question is what an attacker gets if a user imports a hostile one.
//
// The answer here is "a proposal the user has to read and approve":
//
//   - Arguments are an array of [Arg] values written in the template language in
//     template.go, which has placeholders, no functions, no nesting and no
//     evaluation. Nothing in a profile is ever handed to a shell — the executor
//     spawns argv directly — and literals that look like shell control syntax
//     are rejected at validation rather than passed through inert, because a
//     reviewer reading a permission summary should never have to work out for
//     themselves whether `$(curl …)` in an argument is dangerous here.
//   - Matching on tool output is by literal substring, never by regular
//     expression. An untrusted regex is a denial-of-service primitive, and no
//     diagnostic rule needs one.
//   - Filesystem reach is declared as roles ([RootRef]), never as paths. A
//     portable document that contains an absolute path, a home directory, a
//     hostname or a token is refused by [CheckPortable]; where the paths on
//     *this* machine live is a [github.com/andrea-dintino/auto-pigeon-companion/internal/binding.LocalBinding],
//     which is a different type in a different package for exactly that reason.
//   - Importing is inert. A freshly imported profile can do nothing at all
//     until the user has seen its normalized [Diff] and recorded a [Grant]
//     against its exact digest. See trust.go.
//
// # Where the boundaries are
//
//   - This package validates, canonicalizes, diffs, authorizes and resolves.
//     It runs nothing. Supervising processes is the executor's job.
//   - It fetches nothing. A profile names a catalog package; the signed catalog
//     that maps that name to a URL, a size and a digest is separate.
//   - It does not model what a *game* is. A project's engine family, texture
//     model, map dialect and entity vocabulary are a Game Profile, a document
//     AUB owns and AUP already reads. Profiles here carry a [GameProfileRef]
//     — a reference by slug — and never a second copy of that vocabulary.
package profile
