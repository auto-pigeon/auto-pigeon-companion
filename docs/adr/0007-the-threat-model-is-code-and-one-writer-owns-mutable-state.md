# ADR-0007: The threat model is code, and one writer owns every mutable local file

- Status: Accepted
- Date: 2026-09-08
- Affected components: AUCOM
- Decision owners: `AUCOM/AUT 218`

> **Note, 2026-09-23 ([ADR-0008](0008-the-companion-downloads-no-program.md)):** `catalog-state.json`,
> `catalog.SaveState` and `internal/catalog` no longer exist — the Companion
> downloads no program. Section 4 and the `internal/catalog` evidence below are
> history; the one-writer rule for `config.json` and every other mutable file
> stands.

## Context

Two problems arrived together in the hardening pass, and they turn out to be the
same problem seen from two sides: **a security property that nothing checks is a
security property that has already stopped being true, and nobody has noticed.**

### The document side

By `218` the Companion had 646 tests, and most of the hardening prompt's threat
list already had real evidence somewhere in the tree — traversal, bombs,
rollback, revocation, rebinding, CSRF, injection, environment leakage, redaction.
What it did not have was any statement of **which test proves which threat**.

That gap is invisible while the tests pass. It becomes visible on the day
somebody renames `TestExtractRefusesToEscapeThroughAPreexistingSymlink`, or
deletes a test that looked redundant, and no document anywhere notices. A
markdown threat model written that week would have described the program
accurately and then quietly become a description of a program that used to
exist. Every such document does.

### The state side

Every mutable file this program writes was already written atomically — a
temporary file in the destination directory, then a rename. That is right, and it
stops a *torn* write: a `config.json` truncated by a crash or a full disk.

It does nothing at all about a *lost* one. `grep` for `flock|LockFile|lockfile`
across `internal/` and `cmd/` returned nothing. Two Companion processes is not a
hypothetical — the GUI server is one, `companion job run` in another terminal is
a second, and the Windows installer's final page starts the app while a shell may
already have it open. Both read `config.json`, each changes a different field,
both writes succeed, and one change is gone with no error anywhere.

On `config.json` that costs a setting. On `catalog-state.json` it costs a
**revocation** — that file is the replay ratchet and the sticky revocation set,
and it is the one place where losing a write is a security regression rather than
an annoyance.

## Decision

### 1. The threat model is a Go package, and the build checks it

`internal/threat` holds the matrix as data. Each row names its evidence by
package and test function. `threat.Check` **parses every `_test.go` file in the
repository** — parses, rather than greps, because a name in a comment is not a
test and a matrix satisfiable by writing a comment is satisfiable by writing a
comment — and fails when:

- a row names a test that is not in the tree;
- a row has neither an automated test nor a manual procedure;
- a declared category has no row;
- an accepted residual risk has no owner, or its review date has passed.

A row is therefore not a claim that something is impossible. It is a claim that
**a named test would fail if the mitigation were removed**, which is the only
kind of security claim that survives a refactor.

`companion security matrix|residual|audit` publishes it, so the model is
something the shipped program can state about itself rather than something a
reader has to find in a repository.

### 2. Residual risks are owned and dated, and an expiry that passes fails the build

A model that lists only what is fixed is a model nobody learns from. Four risks
are accepted rather than solved, each with a name and a review date. When a date
goes by, `go test ./...` fails.

That is the mechanism, not a defect: **a risk with an expiry nobody has to look
at again is a risk nobody accepted.**

### 3. One writer, through `internal/lockfile`

An `O_EXCL` lock file rather than `flock` or `LockFileEx`, because
`syscall.Flock` is not on Windows and a dependency taken to lock a file in the
user's own config directory would be a poor trade — and this program's empty
module graph is itself a security property (T45).

What that costs is that the kernel does not release the lock when a holder dies,
so four decisions pay for it: the holder is recorded so a refusal can name it;
the lock is refreshed by `Chtimes` so a reader never sees half a record; a stale
lock is broken **through a rename**, so of two processes that both judge it
abandoned exactly one proceeds; and `Release` checks the nonce it wrote, so a
holder whose lock was broken never deletes its successor's.

`config.Update(path, mutate)` is the read-modify-write every caller uses: `mutate`
is handed the file as it is on disk *now*, not as the caller read it earlier. The
web server's persist hook changed shape to match — a mutation, not a value — so a
handler declares the fields it owns instead of writing back a struct it read at
startup.

### 4. The trust state merges, because a lock alone is not enough

`catalog.SaveState` re-reads under the lock and merges: serials take the max,
revocations take the union.

That is not a policy choice layered on top of the file. It is **the same ratchet
the file already is** — every mutation in `State` is monotone, by design and by
its own documentation — so merging can only ever end with more refused than
either writer knew about.

It is necessary because the window between reading that state and writing it back
spans a network fetch of the catalogue, and holding a lock across a network fetch
would make one slow server block every other instance.

A corrupt state file fails the *write* as well as the read: overwriting an
unreadable ratchet is exactly the reset that deleting the file is supposed to be
unable to do quietly.

## Consequences

### Positive

- A renamed or deleted test fails the row that cited it. The model cannot
  silently stop describing the program.
- Writing the matrix found nine claims with nothing behind them, and one row —
  non-ASCII paths — that asserted the opposite of the deliberate behaviour. Both
  are what the check is for.
- Two instances can no longer lose each other's changes, and a revocation
  recorded by one survives a write by the other.
- `companion security audit` answers "what is in this binary" from the binary.

### Negative

- The matrix is maintenance. A test rename is now two edits.
- A build fails on a calendar date nobody edited. That is intended, and it will
  surprise somebody.
- Every write of `config.json` and `catalog-state.json` now takes a lock, which
  is a file creation and a removal per write.

### Risks

- The lock is **not** a security boundary. Any process running as this user can
  delete the lock file — and can also read `config.json` directly, so there is
  nothing here for a lock to defend. It is a correctness mechanism between
  cooperating instances, and its failure mode is a refusal, never a corrupt file.
- A stale-lock window of one minute means a machine that was asleep mid-write can
  have its lock broken. The nonce check is what makes that safe rather than
  merely unlikely.
- A matrix can be kept passing by weakening a row's wording rather than fixing
  the code. `TestNoRowHidesBehindAVagueMitigation` is a partial answer; review is
  the rest of it.

## Alternatives considered

**A markdown threat model.** Rejected for the reason in Context: it is accurate
on the day it is written and has no mechanism for staying so.

**Only a lock, no merge, for the trust state.** Rejected: the read-to-write
window spans a network fetch, so the lock would either be held across it —
blocking every instance behind one slow server — or released before the write,
which is the original bug.

**Only a merge, no lock.** Rejected: two merges can still interleave between the
read and the rename.

**`golang.org/x/sys` for a real advisory lock.** Rejected: the first dependency
this program would take, in exchange for locking a file in the user's own config
directory. The empty module graph is worth more (T45), and the `O_EXCL` design's
one weakness — an abandoned lock — is solvable with the four decisions above.

**Making residual expiry a warning instead of a failure.** Rejected: a warning in
a passing build is a warning nobody reads, and the whole value of an expiry is
that somebody is made to look again.

## Evidence

- `internal/threat` — 50 rows, `Check`, and `TestTheThreatMatrixHoldsUp`.
- `internal/lockfile` — `TestOnlyOneWriterIsEverInsideTheCriticalSection`,
  `TestAnAbandonedLockIsBrokenAndTheTakeoverIsReported`,
  `TestAHolderWhoseLockWasBrokenSaysSoAndDeletesNothing`.
- `internal/config` — `TestAChangeByAnotherInstanceIsNotUndoneByThisOne`,
  `TestConcurrentUpdatesAllLand`.
- `internal/catalog` — `TestARevocationRecordedByAnotherInstanceIsNotOverwritten`,
  `TestConcurrentWritersLoseNoRevocation`,
  `TestACorruptStateFileIsNotSilentlyReplaced`.
- auto-pigeon-tools: `scripts/aucom-security.sh check`, which drives the BUILT
  binary and asks a question this repository cannot ask about itself.

## Follow-up work

- Move the AUB session token out of `config.json` when a keychain can be reached
  without cgo or a dependency (T27, review 2027-03-31).
- Obtain signing certificates and run the procedures already written down (T48,
  review 2027-03-31).
- Extend the lock to `bindings.json` and `license-acceptance.json` if a
  read-modify-write on either ever appears; today both are written whole by one
  path.
