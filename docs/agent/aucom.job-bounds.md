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
