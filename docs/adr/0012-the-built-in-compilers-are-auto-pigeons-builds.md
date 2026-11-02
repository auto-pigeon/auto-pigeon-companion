# ADR-0012: The built-in compilers are Auto-Pigeon's builds, and a program is not migrated

- Status: Accepted
- Date: 2026-11-02
- Decision by: HITL (`NEW_380`)

## Context

The Companion shipped a document for each upstream compiler (ericw-tools 2.0.0-alpha11 for Quake 1, 2.0.0-alpha7
for Quake II, Q3Map2 2.5.17n) and a second, "alternative", document for each Auto-Pigeon fork. Every built-in
pipeline named the upstream document, and the managed installer (ADR-0010) bound both to the fork, so a build that
said "ericw-tools 2.0.0-alpha11 (Quake 1)" could be running release 1.4535. Quake III carried a second leak test
that differed only in the tool it named.

HITL decided that the upstream profiles go and the maintained builds are what is offered.

## Decision

1. **The catalogue holds the three fork documents and no others.** Every bundled pipeline names its game's fork on
   every stage, at version 1.1.0. Pipeline ids are unchanged, so pins, automatic builds and stage arguments keep
   working. `auto-pigeon.q3.leak-test.auto-pigeon-build` became the same document as `auto-pigeon.q3.leak-test` and
   is retired. "Alternative tool" stops being a concept: a stage that names no tool is answered by the fork.
2. **A retired id has one replacement, in one table** (`builtin.ReplacementFor`), and that table is about documents.
3. **Settings move; programs do not.** `internal/retiredprofile` carries folders, the Game Profile and arguments the
   replacement can take, once, recording what it did in `retired-profiles.json`. A bound program stays where it
   was. Nothing here can tell an upstream program from a copy of the fork by its name or banner in a way that
   deserves the word "maintained": that is the managed installer's verified record or the replacement's own check
   on a program a person chose for it. So a replacement nobody bound says it needs setup.
4. **A person's own record is theirs.** A document in the profiles folder declaring a retired id is a `local` profile
   under the ordinary approval rules. A user pipeline whose stage names a retired tool is not rewritten and does not
   fall back: it is told which build to choose.
5. **History is not rewritten.** A build manifest says what ran. The retired binding stays in bindings.json, and
   config.json's pin is mapped when read, so a Companion rolled back by a failed self-update finds what it wrote.
6. **The upstream documents stay as test fixtures** in a package only a test may import, because each fork is held to
   do what its predecessor did and the compatibility rules need a document that accepts what the forks refuse.

## Consequences

- AUT reads `internal/profile/builtin/ericw-tools-q1.tool.json` (`scripts/aucom/resolver.py`, `scripts/ericw/qbsp_profile.py`)
  and its Companion gate binds that id; it has to move to the fork document. Filed in the backlog by `NEW_380`.
- A person who deliberately compiled with upstream can still do so through a profile of their own.
- Rule 5 of `aucom.managed-tools` is unchanged; `bindUnboundToInstalled` only applies it at start to a profile with no
  programs, because an older Companion could leave a verified installation and an unbound fork.
