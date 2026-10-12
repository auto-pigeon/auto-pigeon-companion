# ADR-0009: A program is asked what it is, by the executor, and the answer is evidence about one file

- Status: Accepted — decided by the implementing agent during `NEW_323D`, at night, **for operator review**
- Date: 2026-10-12
- Affected components: AUCOM
- Decision owners: `NEW_323D` (operator review pending)

## Context

The built-in `ericw-tools 2.0.0-alpha11 (Quake 1)` profile passes `-logfile` to
vis and light. Its own "find them on PATH" acquisition note says PATH usually
holds 0.18.1. A binding of that profile to an unpacked 0.18.1 was nevertheless
reported **ready** — by Profiles and by all four built-in Quake 1 pipelines —
because readiness ([ADR-0002](0002-portable-profiles-and-local-bindings-are-different-types.md)'s
binding, read by `engine.Checker`) asked whether the document was approved,
whether a file existed and whether the folders did. qbsp 0.18.1 then compiled
the room and vis 0.18.1 stopped with `Unknown option "-logfile"`
(reproduced at `33df5c3`, 2026-10-12).

A tool profile already carried a `version_probe`, and nothing ran it:
`internal/build/manifest.go` records why — starting a process outside the
executor is what [ADR-0003](0003-one-executor-and-the-record-is-what-says-a-job-ran.md)
forbids — and answers "which version ran" with the executable's digest instead.
That is the right answer to *which bytes ran* and no answer to *are these the
bytes this document was written for*.

## Decision

**The requirement is declared by the document, per executable.** A tool profile
may carry `compatibility_probe`: the arguments to start each named executable
with (none, for ericw-tools), where its identity is in the output, the literal
identities accepted (`prefix`, `contains`), and literal text each executable's
own output must contain (`mentions`, e.g. `-logfile`). Only the selected
profile's own declaration is applied. An executable it does not name is not
probed, and a document with no declaration requires nothing.

**Identities are matched and never ordered.** `1.4505`,
`2.0.0-alpha11-19-g1faae828+auto-pigeon.1.4505` and `2.0.0-alpha11` are three
identities. No semantic-version comparison is made anywhere in this path.

**Asking is a job.** ADR-0003 already lists "a version probe" among the things
the one executor supervises. A probe is a profile action synthesized by
`ToolProfile.ActionByID` (`compatibility-probe.<executable>`; not in
`ActionList`, and not spellable by a document, whose action ids are tokens).
It is admitted by the same code as any job, needs the same approval, and gets
the argument array, empty stdin, bounded capture, time bound, process-group
kill and record every job gets. It is run where it is asked rather than
queued, so a six-hour vis in the queue cannot turn a question into a refusal.
Its exit status is not a verdict — the three measured releases exit 1, 0 and 1
after printing their usage — so a probe that exits has succeeded and its output
is read.

**The answer is evidence about one file, kept in the binding.** A
`binding.ExecutableFact` (binding schema 1.4) records path, size, modification
time, SHA-256, the digest of the requirement it answers, the state, the
identity and the job that asked. A page believes it for the same path, size
and modification time; the executor believes it only for the same SHA-256, read
before anything is started. It is written by `binding.RecordFacts` under the
store's one lock ([ADR-0007](0007-the-threat-model-is-code-and-one-writer-owns-mutable-state.md))
and it never touches `Grant`.

**One decision, enforced in the executor.** `compat.Judge` decides.
`engine.Checker` reports it to Profiles and the pipeline lists;
`job.Service.Submit` refuses before a job exists and the worker checks again
when a queued job starts; `build.Runner.Run` asks about every mandatory stage
before the first.

## Consequences

### Positive

- Profiles, Build & Run, `POST /api/v1/build/runs`, `POST /api/v1/jobs`,
  `companion job run` and `companion build run` give one answer, because the
  refusal is in the one place they all pass through.
- "The program is wrong", "nothing was learned" and "the file cannot be run"
  are three faults with three sentences.
- A compiler replaced where it stands is noticed before it runs.

### Negative

- Setting a toolchain up now starts its programs (once each, with no
  arguments). Each probe is a job record in the Jobs list.
- The binding store is stamped 1.4 the first time this build writes it, and an
  older build then refuses to read it — as with every earlier binding version.
- A profile's "ready" for a TOOL is stricter than "one action can start": any
  program held to the probe that fails it makes the installation need setup.

### Risks

- **The probe runs a program with arguments a document chose.** It is therefore
  refused without the approval running the profile needs, and the built-in
  documents pass no arguments at all. A community document's probe arguments
  are part of what its reviewer approves.
- **`mentions` reads usage text.** A release that supports an option and does
  not list it would be refused. Declared only where measured: vis and light,
  `-logfile`, on 0.18.1, 2.0.0-alpha11, 1.4502 and 1.4505.
- Page-level staleness is size and modification time; a replacement that
  preserves both is caught only by the executor's digest, at the moment that
  matters, and not by the badge.

## Alternatives considered

**Compare `tool_version` with a semantic-version range.** Rejected: the fork's
banner and its release number are not ordered against upstream's, and "newer"
is not "compatible".

**Infer required options from an action's arguments and the program's usage.**
Rejected: it would hold a legitimate 0.18.1 community profile to usage text
nobody measured, and the prompt requires only the selected profile's DECLARED
compatibility.

**A frontend heuristic on the recorded path or folder name.** Rejected: it would
not bind the CLI or the API, and a folder name is not what a program is.

**A separate evidence file.** Rejected: a second mutable local file needs its
own single writer and its own threat-model row, and the binding is already
"what is true of this profile on this machine".

**Remove `-logfile` from the built-in profile, or fall back to another
compiler.** Rejected by the prompt, and by what the log is for.

## Evidence

- Pre-fix reproduction and post-fix refusals: the `NEW_323D` handoff.
- `internal/compat`, `internal/job/compat_test.go`,
  `internal/job/compat_installed_test.go`, `internal/engine/compat_test.go`,
  `internal/web/compat_test.go`, `internal/cli/toolchain_check_cmd_test.go`.

## Follow-up work

- Quake II ericw-tools profiles and Q3Map2 declare no probe; add one with the
  measurement that justifies it.
- Windows and macOS: the wrong-architecture and not-permitted classifications
  read the operating system's own error text and are exercised on Linux only.
