---
id: aucom.job-bounds
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: A bound is not a measurement, and the bound comes from here
authority:
  - aucom-job-retention-bounds
topics:
  - job
  - bounds
  - retention
  - logbuf
  - memory
  - disk
  - stress
  - measurement
  - limits
  - recovery
  - ownership
paths:
  - internal/job/**
---

# A bound is not a measurement, and the bound comes from here

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 500-548 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

## 7. A BOUND IS NOT A MEASUREMENT, AND THE BOUND COMES FROM HERE (`AUCOM/AUT 229`)

`218` wrote T19 — a tool's output is "bounded in memory and on disk" — and `219`
walked a first day without ever testing it. `20260907_229` took the measurement,
and what it left behind binds a later prompt.

**`job.RetentionLimits` is the one published envelope, and it is DERIVED.** Every
field is computed from the constants in `logbuf.go`, never restated beside them,
so changing `headBytes` changes the bound the next measurement is held to in the
same commit. `companion job limits --json` is how a harness reads it. A check
that carries its own threshold passes whatever the program happens to do, which
is exactly how a row can look measured and mean nothing — so **never hard-code a
size into a stress check**, here or in AUT.

**`Kept` and `Resident` are different numbers on purpose.** `KeptBytesPerStream`
is head+tail, what survives the job. `ResidentBytesPerStream` is what a live
capture may hold, which is larger: the tail is compacted at twice its bound
rather than on every append, and a Go slice's capacity is not its length. A
measurement compared against the kept figure would fail a correct build. Do not
"simplify" them into one.

**The line count is not a by-product of the diagnostic rules, and used to be.**
`lineHandler` returns nil when an action declares none, `split` returned
immediately on that nil, and so a job that wrote a megabyte over ten thousand
lines recorded `lines: 0` — beside a `bytes` and a `dropped` that were both
correct, which is what made it invisible for four prompts. Most profiles declare
no rules. `emit` now counts first and offers the line to a rule second;
`TestALineCountIsKeptWhenNoRuleAsksForOne` fails a change that puts the early
return back.

**SIGTERM being ignored is a case that now has a fixture.** Every cancellation
test before this used the `sleep` helper, which dies on the polite signal — so
the grace period elapsing and the forceful pass reaching a whole group were the
two halves of `execution.supervise` nothing had ever run. The fixture traps
SIGTERM three processes deep, and the assertion is that the process GROUP is
empty afterwards, not that the leader is gone: a grandchild still holding the
workspace is the failure the mechanism exists for, and no field of the job record
would report it.

**The measurement is AUT's, and it names the platform it ran on.**
`auto-pigeon-tools/scripts/aucom-acceptance.sh stress` floods a single long-lived
`companion serve` — one process across several rounds, because a fresh process
per flood can only show that one run is bounded and the question is what is
RETAINED between them. `--mode contract` is the CI shape and `--mode measure` is
the native one. Where an operating system will not report comparable RSS the lane
answers `manual_pending` with a command to type; **it never substitutes a file
size, a heap statistic or a reading of the source**, and completion never claims
a platform nobody measured.

## RECOVERY SIGNALS ONLY WHAT THIS STORE PROVABLY LAUNCHED (`NEW_326`)

Q3_017E copied the sandbox of a Companion 44 minutes into a q3map2 LIGHT and
started a second Companion on the copy; its start-up recovery SIGKILLed the
ORIGINAL compiler. The recorded pid and start time were true of the kernel —
that is identity — and nothing asked whether this store had started it — that
is ownership. **Both are checked, immediately before the signal, and only
`Store.assess` in `internal/job/ownership.go` can mint the `ownedOrphan` value
`killAbandonedTree` requires.** Do not add a second route to a signal that takes
a pid read out of a record: a copied store has every pid the original has.

- **What a record proves.** `launch` (`aucom.job/1.5`) is written at admission
  and again, with the program's pid, in the same save that says `running`: the
  store root's canonical path and inode, the job directory's inode, the boot id,
  the pid namespace, the supervisor's pid and start ticks, and the working
  directory relative to the job directory. A signal needs all of them equal to
  what the store READING the record and the kernel say now, the supervisor gone
  (no such process, a zombie, or another start time), the program still its own
  group leader with its start ticks, and — for a program working inside its job
  directory — the kernel's `/proc/<pid>/cwd` being that directory in this store.
  A restored copy cannot have that inode: the orphan is holding it.
- **What a move, a copy, a restore or an older record get.** `interrupted` in
  the store that holds the record, a sentence saying no program was stopped and
  nothing here knows how the run ended, and no signal. A symlink alias resolves
  to the same canonical store and is the same store; a bind mount, a renamed
  directory or another case spelling is not, and is refused — the safe way to be
  wrong. A record without `launch` reads `unverifiable (no_launch_record)`; it is
  not rejected, erased or upgraded.
- **A stale heartbeat is a reason to look, not proof of death.** A supervisor
  that is verifiably alive — asleep, SIGSTOPped, overloaded, its clock moved —
  keeps its job and the record is not written (`live_owner_retained`). An
  inspection error is `unverifiable`, never "dead".
- **Outcomes are tokens**, the same in the record's `recovery`, the serve log
  and the tests: `owned_orphan_stopped`, `live_owner_retained`, `foreign_store`,
  `foreign_boot`, `unverifiable`, `already_exited`, `no_process`, each with a
  bounded reason and the microseconds deciding took. Never a path, a command or
  an environment.
- **The build says what the step knows.** `build.Reconcile` says "the Companion
  stopped" only when the step's recovery outcome is one of this store's own; a
  copied step's build carries the step's own sentence, and the Build page shows
  the manifest's sentence rather than a fixed one. **A manifest read from disk
  is where it was read from** (`build.LoadManifest` sets `Directory`): the live
  copy run found a copy's reconcile saving to the ORIGINAL's absolute path and
  rewriting a build that was still running. A recorded path never sends a
  write back to the store that wrote it — `q3install` already did the same.
- **Outside Linux nothing is signalled**: no dependency-free start time or boot
  id exists there, so recovery marks the job interrupted and leaves the process.
  Do not add a taskkill by pid to "fix" that.
- **The acceptance is at the OS boundary.** `ownership_linux_test.go` and
  `ownership_rows_linux_test.go` run a real supervisor process
  (`helper_supervise_test.go`) and a compiler stand-in held at a barrier, and
  assert on the processes. R01 is the positive control: a change that removes
  every kill passes the copy rows and fails R01.


## A RUNNING TOOL'S CPU AND MEMORY ARE SAMPLED BY ITS EXECUTOR, ONCE (`NEW_365`)

- **One sampler per supervised tool job** (`internal/job/resources.go`), started in `Service.execute` once the
  program has started and stopped when it has been reaped — exit, timeout, cancel or shutdown. Viewers read its
  last reading through `Service.Live`; they never start one, so three tabs are one sampler. Engines (an action
  with a `session_role`) are not sampled.
- **Ownership is the executor's own:** Linux, the process group the tool leads (`Setpgid`), members found through
  `/proc/<pid>/task/*/children` and kept only when their `pgrp` is the tool's and they started after it; the
  tool itself by pid AND start ticks, so a reused pid ends the reading. Windows: the tool's process only (the
  executor has no Job Object), scope `root_process`. Elsewhere: `unavailable`, never zero. No `ps`, no
  `tasklist`, no privilege.
- **Bounded:** one reading and one previous counter set in memory per job; the job record keeps only a
  `resources` summary (`aucom.job/1.6`: peaks, sample count, scope) — no time series. Measured on ubuntu24:
  one sample ≈ 150 µs of CPU, ≈ 0.015 % of one core at 1 Hz (`TestOneSampleCostsWellUnderOnePercentOfACore`).
- **Never a verdict.** A failed sample is a timestamped `unavailable`, never stops the job, its output drains, its
  cancellation or shutdown. Percent = CPU time / wall time × 100 per logical CPU; the first reading is
  `measuring`. What the tool printed about progress (`progress.go`) is a separate reading, per phase.

## A BACKGROUND CHILD OPENS NO CONSOLE WINDOW ON WINDOWS (`NEW_368`)

- **`internal/noconsole` is the one policy.** A Companion started from Explorer, a protocol link or its own
  update has no console to share, so Windows gave every console program it started — a version probe, the
  converter's handshake, `taskkill`, `reg.exe`, `cmd /c start` — a blank window of its own. `noconsole.Apply`
  adds `CREATE_NO_WINDOW` to whatever creation flags a launch already has (the job's
  `CREATE_NEW_PROCESS_GROUP` is kept); output still goes to the caller's pipes, the exit status and the
  taskkill tree stop are unchanged. Never `HideWindow`/`SW_HIDE` as the mechanism (it can still flash), never a
  `cmd /c` or PowerShell wrapper.
- **Who is background:** a tool job (`job.configureLaunch`: no `session_role`), the AUE runner (handshake and
  conversions), the self-update proving start, `taskkill`, `reg.exe` (urischeme), the browser opener and the
  file-picker helper (its dialog is its own GUI window; only the console goes). **Not background:** a session
  job (client, listen or dedicated server — a person plays or watches it), `game join`'s engine, and anything
  started `DETACHED_PROCESS` (the self-update relaunch, `game open`), with which Windows does not allow the
  flag — `noconsole.Flags` leaves those as they are.
- **Not covered:** the console of `companion.exe` itself. It is a console-subsystem program so the CLI keeps
  its terminal, stdout and exit codes; a protocol-link launch that forwards to a running Companion therefore
  still gets a console for its own short life. Whether that is visible, and the GUI-subsystem launcher it
  would take, is a native-Windows observation (prompt 02), not something cross-compilation shows.

