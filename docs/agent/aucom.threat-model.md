---
id: aucom.threat-model
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: The threat model is code, and there is one writer of a mutable local file
authority:
  - aucom-threat-model
  - aucom-single-writer-lockfile
topics:
  - threat
  - security
  - risk
  - matrix
  - review-date
  - lockfile
  - mitigation
  - evidence
  - adr
paths:
  - internal/threat/**
  - internal/lockfile/**
  - docs/adr/**
---

# The threat model is code, and there is one writer of a mutable local file

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 369-446 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

## 5. THE THREAT MODEL IS CODE, AND THERE IS ONE WRITER (`AUCOM/AUT 218`)

`20260906_218` was an evidence pass, and the two things it left behind are things
a later prompt can undo by accident. `docs/adr/0007` is the record; this is what
binds a change.

**`internal/threat` is checked by the build, and that is the point.** Each of its
50 rows names the tests that are its evidence, and `threat.Check` PARSES every
`_test.go` in the tree — parses, not greps, because a name in a comment is not a
test and a matrix satisfiable by writing a comment is satisfiable by writing a
comment. **If you rename or delete a test, fix the row that cited it.** Do not
delete the row: a row that has lost its proof is information, and removing it is
how the model becomes a description of a program that used to exist. A row is not
a claim that something is impossible; it is a claim that a named test would fail
if the mitigation were removed, which is the only kind of security claim that
survives a refactor.

**A residual risk has an owner and a review date, and an expired one FAILS THE
BUILD.** That is the mechanism. When `TestTheThreatMatrixHoldsUp` fails on a
date, look at the risk, decide again, and move the date or fix the risk — never
delete the entry to make it pass, and never soften a `Mitigation` string instead
of fixing code. A model that lists only what is fixed is a model nobody learns
from, and `TestTheMatrixStillSaysWhatIsNotSolved` fails an emptied register.

**Every mutable local file has one writer.** `internal/lockfile` is O_EXCL rather
than flock because `syscall.Flock` is not on Windows and a dependency taken to
lock a file in the user's own config directory would cost the empty module graph
that T45 is about. Use `config.Update(path, mutate)` for a read-modify-write —
never Load, change, Save, which is the lost update this replaced. The web
server's persist hook is a MUTATION, not a value, for the same reason; a handler
declares the fields it owns. `AUCOM/AUT 228` extended the same rule to
`bindings.json`: every write goes through `binding.Update(path, mutate)`, because
that file holds the GRANTS, and a lost update there costs an approval or brings
back one somebody withdrew.

**`catalog.SaveState` merges, and the merge is not optional.** Serials take the
max, revocations take the union. That is the same ratchet the file already is,
not a policy on top of it, and it exists because the read-to-write window spans a
network fetch — a lock alone would either block every instance behind one slow
server or be released before the write. A corrupt state file fails the WRITE as
well as the read.

**Zero external Go dependencies is a security property, not an aesthetic.**
`release.TestThisProgramLinksNoExternalModule` reads the module graph out of the
BINARY with `debug.ReadBuildInfo`, because `go.mod` states an intent and the
binary states a fact. Adding a module is a decision about the artifact's licence
and its supply chain: record it in `THIRD_PARTY_NOTICES.md` and in the matrix, in
the same change.

**A URL handler runs `game open` — which has no approval flag — on every platform.** The
URL arrives as one argv element through a field code, quoted on Windows, with no
shell anywhere. Never register a command that launches on arrival, and never add
a flag to the handler. `internal/urischeme` performs the Linux and Windows
registrations and REFUSES the macOS one, because LaunchServices reads the
declaration out of the bundle and there is no supported way to register a scheme
for a loose binary — inventing a third way is the failure mode. `Env.URIRegistrar`
exists so no test ever touches the desktop of the machine running it.

**A package script deletes nobody's data.** It runs as root on a machine that may
have several users. `companion uninstall --purge --confirm` is the per-user
command, it lists everything first, and it refuses a directory whose name is not
`auto-pigeon-companion` — because `AUCOM_JOBS_DIR` pointing at `~/projects` must
not make purging delete `~/projects`.

**Nothing here is signed, and every document says so.** No Apple Developer ID and
no Authenticode certificate exist. The procedures are written down in
`build/macos/make-app-bundle.sh` and `build/windows/installer.iss`; the
verification workflow reads no secret and a job of its own fails the moment one
is added. Do not make the packaging imply otherwise, and do not put a key in the
workflow a pull request runs.

**AUT asks the question this repository cannot ask about itself.**
`auto-pigeon-tools/scripts/aucom-security.sh` builds the binary and drives it
through its documented CLI with `HOME` and the XDG variables pointed into a
sandbox — the operating system's own mechanism, so no test-only fallback had to
be added here. A change that makes a lane pass by adding an environment variable
only a test reads is undoing that.
