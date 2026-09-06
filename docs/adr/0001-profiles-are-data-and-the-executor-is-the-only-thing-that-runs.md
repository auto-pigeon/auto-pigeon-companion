# ADR-0001: Profiles are data, and the executor is the only thing that runs

- Status: Accepted
- Date: 2026-09-06
- Affected components: AUCOM
- Decision owners: `AUCOM 204`

## Context

The Companion drives programs it does not own: map compilers, visibility and
lighting stages, game engines, packagers. Each is a separate process under its
own licence — that separation is what lets an MIT repository drive GPL tools —
and each needs a different command line on a different platform with a different
set of files.

There are two shapes for that, and the choice is made once.

**The first is a switch.** `if tool == "qbsp" { … }` in the builder,
`if engine == "ironwail" { … }` in the launcher. It is the shape that appears by
itself when the first tool is wired up, and it has two costs that only arrive
later. A third tool becomes a code change, a release and a download rather than
a document. And the tools that ship become a privileged path: they run through
code no user-supplied configuration can reach, so the extension model — if one
is ever added — is exercised by nobody who wrote it, and is discovered to be
insufficient the first time somebody tries to use it.

The second shape is a document format, and it raises the question this ADR
exists to answer: **what is a profile allowed to be?** Because the honest
description of "a file that tells the program what command to run" is *a
program*, and a program that arrives from another person is the thing every
plugin system has eventually regretted being.

The pressure towards that is real and specific. A tool needs an unusual flag on
one platform; the quick answer is a per-platform shell string. A user needs an
option the profile's author did not anticipate; the quick answer is a free-text
"extra arguments" box. A compiler's output needs parsing; the quick answer is a
regular expression in the document. Each is one member, each solves a genuine
problem, and each individually destroys a property the rest of the design
depends on.

## Decision

**One execution model. Profiles are declarative data. The built-in profiles go
through the same path as everything else.**

Concretely:

1. **A profile describes; the executor runs.** `internal/profile` decodes,
   validates, canonicalizes, digests, diffs, authorizes and *resolves*. It
   starts no process, opens no socket and touches no file. `Resolve` produces an
   `Invocation` — an executable, an argument array, a working directory, an
   environment, the roots the process may reach — and hands it over. Supervising
   that is the executor's job and is not in this package.

2. **A command is an executable reference and an argument array.** Never a
   string. Nothing a profile contains is passed to a shell, because there is no
   shell anywhere in the path: the executor spawns argv directly.

3. **The template language has placeholders and nothing else.** `{namespace.name}`
   substitution, no nesting, no functions, no arithmetic, no defaults syntax,
   no evaluation. A conditional argument is `when: {option: …}` on the argument
   — total, inspectable, and renderable in a command preview.

4. **Overrides are declared, typed options.** There is no free-text argument
   member anywhere in the format. One would make every declared root, network
   need and environment statement decorative, because the user could hand the
   tool `-o /etc/anything` and the profile would no longer determine the
   command.

5. **Output matching is literal.** `contains`, `prefix`, `suffix`. No regular
   expressions: an untrusted regex is a denial-of-service primitive that needs
   no privileges and no exploit, and no compiler diagnostic needs one.

6. **Filesystem reach and network need are declared, in roles, per action**, and
   an action's process inherits no environment except the variables it names.
   `PATH` and the loader variables may not be named at all.

7. **Built-in profiles are documents, not code.** They are embedded with
   `//go:embed` and read by the same decoder, validated by the same rules and
   resolved by the same function as a file somebody downloads. Trust decides
   whether a grant must be collected; it decides nothing about what the document
   may say or how it is executed.

The last point is asserted rather than described:
`TestBuiltinAndUserAuthoredResolveToTheSameCommand` resolves a built-in sample
and a deliberately differently-spelled user-authored equivalent and compares
`Command.Digest()`. If a privileged path is ever added, that test is what fails.

## Consequences

### Positive

- A third tool, or a fifth engine, is a document. It needs no release.
- Whatever a hostile profile can express, the worst outcome is a proposal the
  user is asked to approve, described in a permission summary derived from the
  same members the executor enforces.
- A command preview is worth showing, because the previewed value is the value
  that runs.
- The published JSON Schema documents are a real contract: an editor, a CI check
  or a second implementation can validate a profile without this program.

### Negative

- Some tool will want something the format cannot express, and the answer will
  be a format change with a schema version rather than an escape hatch. That is
  slower, and it is the cost being deliberately paid.
- Two descriptions of the format exist — the schemas and the Go types — and they
  can drift. Mitigated by deriving one from the other in a test rather than by
  intention.
- Literal-only diagnostic matching means some tool output is not classified as
  finely as a regex would manage. The raw log is always kept, so nothing is lost
  beyond convenience.

### Risks

- **An "extra arguments" member is added under pressure.** This is the most
  likely way the decision is undone, and it would be undone quietly, because the
  member looks small. The schema, the Go types and the ADR all say why it is
  not.
- **A built-in shortcut is added for performance or convenience.** Guarded by
  the equality test above.
- **The template language grows.** Each addition is individually reasonable; the
  aggregate is an interpreter. A change here should have to justify itself
  against this section.

## Alternatives considered

- **A scripting language (Lua, Starlark, a JS sandbox).** Expressive, and it
  moves the whole question to "is the sandbox sound", which is a much larger
  ongoing commitment than "is this JSON valid" — and it would need a dependency
  in a repository that has none.
- **Shell strings with careful quoting.** The quoting is never careful enough,
  and the resulting profile is unreadable to the person being asked to approve
  it.
- **A hardcoded registry of blessed tools, extension deferred.** This is the
  status quo the repository already has in `internal/tools`, and deferring the
  model is what makes the eventual model wrong: it gets designed around the
  tools that were already special-cased.

## Evidence

- `internal/profile/doc.go`, `template.go`, `action.go`, `resolve.go`.
- `internal/profile/testdata/malicious/` — 25 documents that must be refused,
  each with the sentence a user should be shown.
- `internal/profile/builtin/builtin_test.go` —
  `TestBuiltinAndUserAuthoredResolveToTheSameCommand`.

## Follow-up work

- `AUCOM 205` builds the executor on `Invocation`, and migrates
  `internal/tools` and `internal/launch` onto it rather than beside it.
- `AUCOM 206` adds the signed acquisition catalogue the `managed_download`
  mode names.
- `AUCOM 207` and `AUCOM 209` replace the built-in samples with qualified
  EricW and engine profiles.
