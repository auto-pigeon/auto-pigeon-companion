# ADR-0003: One executor, and the record — not a process — is what says a job ran

- Status: Accepted
- Date: 2026-09-06
- Affected components: AUCOM
- Decision owners: `AUCOM 205`

## Context

[ADR-0001](0001-profiles-are-data-and-the-executor-is-the-only-thing-that-runs.md)
settled that a profile is data and something else runs it. This record settles
what that something else is, because when 205 began there were three of them.

`internal/tools` had a `RunProcess` and a `Manager.Run`. `internal/launch` had a
`Run` that called the first one for game executables. The fake tool had a third
`Run` that printed lines instead of starting a process — so the one execution
path a user could actually exercise was the one that never spawned anything.
Each was short and each was reasonable where it stood.

The cost of three is not that the code is repeated. It is that **every guarantee
has to be made three times, and is only true where somebody remembered to make
it**. A containment check added to the compile path does not protect the launch
path. An argument array that is never joined into a string in one function says
nothing about the other two. A timeout, a bounded log, a process-group kill, a
record of what ran: each is a property of a code path, not of the program, and a
user reading "the Companion never uses a shell" has no way to know which path
their button went down.

The second question is what happens when the supervisor dies. A job is a process
on somebody's machine, and a laptop lid closes. The record then says `running`
and nothing is running, and the two obvious repairs are both wrong: believing
the record leaves a build in progress forever, and re-running the command
decides on the user's behalf that doing it twice is safe. The Companion does not
know whether the compiler got far enough to matter.

## Decision

**One executor.** `internal/job` supervises every process this program starts.
A compile, a game, a version probe: same queue, same states, same workspace
rules, same logs, same record. `Submit` is the only entry point, and there is no
second one for a built-in profile, for the CLI, for the GUI, or for "just this
once". `tools.RunProcess`, `Manager.Run`, `tools.Build`, the fake tool's `Run`
and `launch.Run` were deleted rather than wrapped.

A launch config reaches the executor by becoming an engine **profile**, so that
starting a game and starting a compiler are the same act on the same type. That
generated document is validated, canonicalized and digested like any other; it
is trusted the way an embedded one is, because this build produced it from
configuration the user already had.

**Eight states and one transition table.** `queued`, `resolving`, `running`,
`cancelling`, `succeeded`, `failed`, `cancelled`, `interrupted`. Every change of
state goes through `Transition`, including a move to the state a job is already
in, which is refused: a double-cancel and a race between two goroutines
finishing the same job are both worth failing loudly rather than absorbing.

**A crash is admitted, not repaired.** An unfinished job whose owner stopped
saying it was alive becomes `interrupted`, which means *nobody knows how this
ended*. Running it again is `Retry`, it is a **new** job, and it is the user's
decision. That applies to a `queued` job too, which demonstrably never started:
the conservative answer is uniform, and the user who wants the other one has a
command for it.

**Ownership is a heartbeat, not a pid.** The owner touches a file beside the
record every two seconds; a job whose heartbeat is thirty seconds old is
abandoned. A pid is reused, and "process 4212 exists" is not evidence that it is
the Companion that started this job. This is what lets `companion job list` in a
terminal share a store with a running GUI server without taking its jobs away,
and what lets `companion job cancel` stop a build the server owns.

## Consequences

### Positive

- A guarantee stated in `internal/job`'s documentation is a guarantee about the
  program. "There is no shell", "nothing is inherited", "outputs are contained",
  "a job is recorded" are true for every caller because there is one caller.
- The fixtures are worth something. They re-execute the test binary as the
  external program, so success, a nonzero exit, a missing executable, a timeout,
  a cancellation from another process, an orphaned grandchild, a four-megabyte
  flood and invalid UTF-8 are all exercised against a real process with a real
  exit status — not against a fake that printed.
- The GUI and the CLI cannot disagree about a job, because neither owns one.
  Both read the same record from the same store.
- `companion launch` starting a supervised, listed, cancellable process is a
  capability it did not have, obtained by deleting code rather than adding it.

### Negative

- Everything now costs a profile. A quick "just run this binary" has nowhere to
  go, and the launch bridge exists precisely because that was not acceptable for
  a feature that already worked.
- The generated engine profile is a second, lesser description of something
  `AUCOM 209` will describe properly. It has to be removed then, not extended.
- A job directory per run is more filesystem state than a subprocess call, and
  nothing prunes it yet.

### Risks

- **The Windows process tree is the weakest part.** Job Objects need
  `golang.org/x/sys/windows`, which this repository does not take, so the tree is
  taken down with `taskkill /T` at an absolute path. It is the documented
  mechanism and it is argv-direct, but it is a different mechanism from the Unix
  process group and it is not exercised by CI's Linux fixtures.
- **This is not a sandbox and must never be described as one.** The containment
  checks stop a *document* from directing a program outside its declared roots,
  and stop the Companion from reading or publishing anything outside them. They
  do not stop an authorised program from writing where the user can write. The
  README says so in those words; a future summary that drops the qualifier would
  be the failure mode.
- The heartbeat interval and the staleness window are numbers, not proofs. A
  machine suspended for a minute mid-build comes back to an `interrupted` job
  that was actually fine.

## Alternatives considered

**Keep the three runners and add the guarantees to each.** Rejected: that is the
state 205 found, and the reason each guarantee existed in one place and not the
others.

**One runner, but a `Spec` type that both a profile action and a raw plan can
produce.** Tempting, and it would have avoided the generated-profile bridge. It
was rejected because the raw plan would immediately become the path anything
awkward took, and within two features it would be a second model of what a
command is — the switch from ADR-0001, one layer down.

**Interrupt only `running` jobs on recovery, and resume `queued` ones.** A
`queued` job provably started nothing, so resuming it is safe in a way resuming a
`running` one is not. Rejected for uniformity: "the Companion never re-runs a
command you did not ask it to" is a sentence with no exceptions, and one
exception is what makes people stop believing the rest.

**A random token in the URL the browser opens** (the Jupyter shape), which the
earlier `internal/web` TODO proposed. Rejected: a URL reaches browser history, a
`Referer` header, and a shell's scrollback. The token goes in a header that the
page reads from its own markup and a second process reads from a 0600 file.

## Evidence

- `internal/job/state.go` — the table; `TestEveryStateTransitionIsDecidedByOneTable`
  asserts it against a list written out separately.
- `internal/job/exec.go` — argv-only execution;
  `TestAnInjectionPayloadIsOneLiteralArgvElementAndNeverRuns` runs ten payloads
  through a real process and checks both the argv it received and that nothing a
  shell would have done happened.
- `internal/job/store.go` — the heartbeat;
  `TestARestartMarksAnAbandonedJobInterruptedAndNeverRerunsIt` and
  `TestRecoveryLeavesAJobAnotherProcessIsStillSupervising`.
- `internal/job/logbuf.go` — always drain, bound what is kept;
  `TestAnOutputFloodIsBoundedInMemoryAndOnDisk`.
- `internal/launch/profile.go` — the launch bridge;
  `TestEveryExampleConfigBecomesAValidEngineProfile` puts the generated document
  through the same validation as a published one.
- `git log` for `AUCOM 205` — the three runners removed in the same change that
  added the one.

## Follow-up work

- `AUCOM 206` gives acquisition a signed catalog; `internal/tools` is the half
  this record left in place.
- `AUCOM 209` replaces the generated launch profiles with curated engine
  profiles. When it lands, `internal/launch/profile.go` should go, not stay.
- Nothing prunes finished job directories. A retention policy belongs with the
  job UX in `AUCOM 212`.
- The Windows process-tree path wants a machine to run on before it is trusted.
