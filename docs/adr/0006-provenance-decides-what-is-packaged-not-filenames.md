# ADR-0006: Provenance decides what is packaged, and filenames decide nothing

- Status: Accepted
- Date: 2026-09-07
- Affected components: AUCOM
- Decision owners: `AUCOM 208`

## Context

[ADR-0005](0005-a-pipeline-is-several-jobs-and-a-manifest-is-what-says-so.md)
got a BSP out of a supervised pipeline with a manifest beside it. The step after
that is the one a mapper actually publishes: **put it in a PAK or a PK3 so
somebody else can install it.**

Two problems arrive together at that step, and they are usually solved badly.

**The first is that packaging is where a build stops being reproducible.** Every
archive format in this family is old enough to have been designed when a
timestamp and a permission bit were free. ZIP records the modification time, the
packing platform, the packer's umask and an Info-ZIP extended-timestamp extra
field; Go's `archive/zip` writes all four by default. So two people packaging
byte-identical inputs get different archives, and a project that has gone to the
trouble of a recipe key throws the property away in its last command.

**The second is legal, and it is the one that gets tools into trouble.** The
working directory for making a Quake map is very often the directory Quake is
installed in. `--from .` in that directory sweeps up `gfx/palette.lmp`,
`progs.dat` and possibly `pak0.pak` itself, and the resulting upload
redistributes id Software's content. Every tool that has tried to prevent this
has reached for the same mechanism — a list of forbidden filenames — and the
mechanism is wrong in three separate ways:

- it **refuses legitimate content**: a total conversion's own `progs.dat` is the
  author's work and is refused on the strength of its name;
- it **passes the thing it exists to stop**: `cp pak0.pak mystuff.dat` defeats it
  completely;
- worst, it **implies a conclusion it cannot support**. A tool that says "checked,
  no proprietary assets found" has made a legal claim on the strength of a string
  comparison, and the user believes it.

## Decision

**1. A packaging target states its reproducibility promise, and the promise is
measured.**

Two promises, named in the type and written into every sidecar manifest:

- `portable` — PAK, and PK3 with `store`. The same members produce the same
  bytes on any machine and under any build of the Companion. Nothing
  machine-dependent and nothing library-dependent reaches the byte stream.
- `per_build` — PK3 with `deflate`. Byte-identical under one build of the
  Companion on any operating system, and not promised across two, because the
  compressor belongs to the Go standard library.

Everything ZIP would otherwise record about the packing machine is pinned: the
MS-DOS epoch in the legacy date and time fields, `Modified` left zero so
`archive/zip` writes no extended-timestamp extra, `CreatorVersion` zero,
`ExternalAttrs` zero, members sorted byte-wise on the path.

**2. What a candidate is packaged on is where its bytes came from, ordered by
strength of evidence.**

First match wins:

| Rule | Evidence | Verdict |
| --- | --- | --- |
| `known-asset-digest` | SHA-256 is a released commercial file's | refuse |
| `authorized-known-asset` | the same, plus an explicit recorded authorization | include |
| `authorized` | an explicit recorded authorization | include |
| `build-output` | a build manifest records this exact content | include |
| `game-content-root` | selected out of an installed game's directory | review |
| `authored-root` | from a directory the user declared as their own | include |
| `unknown-provenance` | nothing above | review |

`review` is a first-class outcome, not a failure: it is an unanswered question,
held until a person answers it. `create` refuses a plan with one outstanding.

**3. Filenames are hints, they decide nothing, and they say so themselves.**

A hint is printed beside a decision that was already made on other grounds, and
every hint's own text disclaims itself ("that is a name, and it decides
nothing"). No verdict is ever reached from a filename.

**4. The built-in known-asset corpus ships empty.**

Content identity is the only exact rule available, and it requires digests
somebody actually computed from released media. This repository has computed
none. Shipping plausible-looking hashes would be strictly worse than shipping
none: a rule that never fires, wearing the costume of one that does. A supplied
corpus must declare its own `source`.

**5. The package manifest goes beside the archive, never inside it.**

No built-in target permits Auto-Pigeon metadata inside a game archive. The
mechanism exists for a profile-supplied target that says otherwise, and an
`--embed-manifest` against a target that does not permit it is refused with an
explanation rather than ignored.

## Consequences

### Positive

- A published archive is reproducible, and its manifest says under what
  conditions rather than leaving a reader to assume.
- The mechanism that catches the real accident — sweeping a directory that is
  also a game directory — needs no corpus, no list and no maintenance, and works
  on renamed files.
- Every decision is explainable in one sentence naming the rule that made it, so
  a user who disagrees knows what to argue with.
- The reason a person gives for an authorization is recorded verbatim in a
  published artifact.

### Negative

- The default is more friction than a filename blacklist: a user packaging
  hand-made content from an undeclared directory is asked to acknowledge it.
  That is the intended trade — the alternative is a tool that silently packages
  whatever it is pointed at.
- `--acknowledge-all` exists because the friction would otherwise be unusable at
  scale. It requires `--reason` and names every path it covered, which is the
  most that can be done about a bulk affirmation.
- Two reproducibility promises rather than one is a distinction a reader has to
  absorb. Collapsing them would mean either giving up deflate or making a claim
  that is not true.

### Risks

- **The empty corpus reads as an unimplemented feature.** Mitigated by saying so
  at the point of definition, in the README and here; the protection is the two
  `review` rules, and they are what the tests exercise.
- **`--acknowledge-all` becomes the habit.** Nothing prevents it. What exists is
  a record: the manifest lists every path it covered and the reason given.
- **This is not legal advice and could be read as such.** The README says
  explicitly that the program reports what it knows about where bytes came from
  and declines to guess when it knows nothing.

## Alternatives considered

- **A filename blacklist.** Rejected for the three reasons in Context. It
  survives only as `Hints`, which decide nothing.
- **Refusing everything from a game directory.** Rejected: a user's own mod
  lives there too, and building straight into `id1/maps/` is ordinary. `review`
  is the honest verdict for evidence about location.
- **Ordering the corpus rule below the build-output rule.** Rejected: content
  identity beats location, and a released file does not become the author's by
  being copied into a build directory. The authorization override was added
  instead, and it keeps the identification in the record rather than replacing
  it.
- **Wrapping `q1tools` or QPakMan.** Rejected by the prompt and on its own
  merits: the archive layer is small, and shelling out at the last step would
  give up the reproducibility and the provenance record that everything upstream
  exists to produce. They remain good interactive tools and are documented as
  such.
- **Writing the manifest into the archive.** Rejected: an engine has no use for
  it, and it publishes the packager's toolchain to everyone who downloads the
  map.

## Evidence

- `internal/pack/golden_test.go` pins the two `portable` digests and varies
  modification times, permission bits, path separators, walk order and source
  path at once, asserting the bytes do not move.
- `internal/pack/policy_test.go` exercises every rule, including that content
  identity outranks a build-output match and a declared root, and that a hint
  never reaches a verdict.
- `internal/pack/pak_test.go` parses written archives with a second, independent
  decoder that shares no code with the writer; `pk3_test.go` checks PK3s with
  the system `unzip` where it is installed.
- Two design faults were found by these tests rather than by review: an
  authorization could not override a corpus refusal, and a symbolic link already
  present *inside* an extraction destination defeated the resolved-root check.

## Follow-up work

- `AUCOM 217` gives external community tools, `q1tools` among them, a catalogue
  entry of their own.
- A real known-asset corpus, computed from media somebody actually holds, is a
  data task and not a code one. The format and the loader are in place.
