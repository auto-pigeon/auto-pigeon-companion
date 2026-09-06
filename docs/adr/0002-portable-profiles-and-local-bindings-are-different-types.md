# ADR-0002: A portable profile and a local binding are different types in different packages

- Status: Accepted
- Date: 2026-09-06
- Affected components: AUCOM
- Decision owners: `AUCOM 204`

## Context

A profile has to say two things that look like one thing:

- **What the tool is** — its identity, its licence, which programs it provides,
  what arguments they take, what they read and write. True everywhere.
- **Where it is on this computer** — `/home/andrea/tools/ericw/qbsp`, the game
  folder the user picked, the version the probe reported last Tuesday, the
  permissions this person approved, and which AUB record the Game Profile slug
  resolves to on this account. True nowhere else.

Merging them is the obvious first design, and it fails in both directions. A
shared document containing somebody's home directory is wrong on every machine
that reads it. A shared document containing a token has disclosed it. And a
document that mixes the two makes "publish this profile" a decision about which
members to strip — a decision that is made correctly the first time and
incorrectly the fourth, when somebody adds a field.

This workspace has already paid for the near-miss version of this. `20260807_02`
was a compiled-in `127.0.0.1:5174` that turned a misconfiguration into a
plausible wrong answer on a LAN, where loopback means *the reader's own
machine*. The rule that came out of it — component addresses live in `.env`,
never in code — is the same rule as this one, applied to a different artifact: a
document that travels must not carry a location.

## Decision

**Two types, in two packages, with a one-way import.**

```text
internal/binding  ──imports──▶  internal/profile
```

`internal/profile` holds the portable document. `internal/binding` holds
`LocalBinding`: absolute executable paths, absolute root paths, the resolved
tool version and when it was checked, the AUB Game Profile *record id*, the
user's `Grant`, and their overrides. Nothing imports `binding` from `profile`,
so **no type in a profile can contain a binding** — Go refuses the cycle. The
guarantee is the package graph, not a comment, and
`TestProfilePackageDoesNotImportBinding` asserts the direction so a refactor
cannot quietly reverse it.

The rules in each package are inverses, and each says so:

| | portable profile | local binding |
|---|---|---|
| paths | roles only; an absolute path is refused | absolute only; a relative path is refused |
| Game Profile | AUB **slug** + engine family | AUB **record id** |
| network | DNS names; an IP literal is refused | n/a |
| credentials | refused | n/a — a grant is a decision, not a secret |
| workspace | a role a job fills in | never stored; a job creates and destroys it |

The type system cannot catch the *content* mistake — a person pasting a path
into a `purpose` string — so `profile.CheckPortable` scans every string of the
canonical form for the four things that must never travel: an absolute path
(POSIX, drive-letter or UNC), a home reference, a network location (loopback,
private, or any IP literal), and anything shaped like a credential. It runs
inside `Validate` and inside `Canonical`, so a document cannot be validated,
digested, exported or imported without it.

**The Game Profile reference is by slug, and this is the load-bearing part of
the split.** AUB owns what a project is — engine family, map dialect, texture
model, entity vocabulary — and AUP already reads that document rather than
branching on a game name. The Companion references it and does not restate it.
A record id would have been the precise answer *on one deployment*: portable
documents get the slug, and resolving it to a record id is a binding's job. The
portable schema has no `profile_id` member at all, so the mistake is not
detected, it is unrepresentable.

## Consequences

### Positive

- "Can this be published?" is answered by the type system and one function,
  not by a reviewer remembering which members to strip.
- A profile can be shared by copying the file. There is nothing to sanitize.
- A binding can be deleted and rebuilt — reinstall a tool, pick a different game
  folder — without touching the document, and a document can be updated without
  losing where the user's things are.
- Grants are keyed by document digest, so a binding can say precisely whether
  what is installed is still what was approved.

### Negative

- Two schema versions to maintain (`aucom.profile/1.0` and
  `aucom.local-binding/1.0`). Deliberate: they have different compatibility
  obligations, and tying them together would force a local-state migration every
  time the published format moved.
- Resolution needs both halves, so `profile.Resolve` takes a `Request` the
  binding builds. That indirection is the price of the import direction.
- `CheckPortable` is a heuristic on the content side and will occasionally
  refuse something innocent. It is a per-string refusal with the offending text
  named, so the cost is one edit, not a mystery.

### Risks

- **A convenience field.** "Just cache the resolved path in the profile so we do
  not have to look it up" is how the split gets undone, and it would look like a
  small optimisation. The import direction makes it fail to compile, which is
  the intended outcome.
- **A profile carrying an AUB record id in a differently-named member.** Nothing
  structural prevents inventing `aub_id` in a future schema version. The
  reasoning is here so the next author has to disagree with it on purpose.

## Alternatives considered

- **One document with a `local` block stripped on export.** Correct until
  somebody adds a member outside the block. The failure is silent and the
  artifact has already been shared by the time it is noticed.
- **A marker interface (`NonPortable`) with a reflective check on export.**
  Catches the structural mistake, at run time, if the check is called. The
  import direction catches it at compile time, always.
- **Referencing the Game Profile by record id and translating on import.**
  Requires an AUB round trip to read a document, makes an offline import
  impossible, and gives a wrong answer rather than no answer when the same id
  exists on a different deployment.

## Evidence

- `internal/binding/doc.go` — the import-direction guarantee in prose.
- `internal/profile/portable.go` — `CheckPortable`.
- `internal/profile/gameprofile.go` — why the reference is a slug.
- `internal/binding/binding_test.go` —
  `TestNothingFromABindingCanEnterAPortableProfile`,
  `TestAnAubRecordIdHasNowhereToGoInAPortableProfile`.
- `internal/profile/schema_test.go` — `TestProfilePackageDoesNotImportBinding`.

## Follow-up work

- `AUCOM 205` stores bindings and grants where the executor can read them.
- `AUCOM 210` resolves the Game Profile slug against AUB, which is where the
  record id in a binding starts being filled in from something real.
