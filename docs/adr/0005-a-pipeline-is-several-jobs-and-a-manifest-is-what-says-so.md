# ADR-0005: A pipeline is several jobs, and a manifest is what says so

- Status: Accepted
- Date: 2026-09-07
- Affected components: AUCOM
- Decision owners: `AUCOM 207`

## Context

[ADR-0001](0001-profiles-are-data-and-the-executor-is-the-only-thing-that-runs.md)
settled that a profile is data.
[ADR-0003](0003-one-executor-and-the-record-is-what-says-a-job-ran.md) settled
that one executor runs it and the job record is what says it ran.
[ADR-0004](0004-acquisition-is-verified-or-it-does-not-happen.md) settled how
the program gets onto the machine. What none of them settled is the thing a
mapper actually asks for: **compile this map**, which is three programs in
order, each reading what the last one wrote.

The pipeline *document* existed — `internal/profile` had `PipelineProfile`,
capabilities, wires and a resolver — and nothing ran one. Writing the first real
toolchain profile is what forced the question, because the real toolchain does
three things the sample toolchain had quietly not modelled:

- `qbsp` writes a `.prt` beside the `.bsp` it was told to write, and `vis` opens
  the `.prt` **of the same stem in the same directory** without being handed it.
  The executor stages each input into its own directory, which is right for two
  unrelated files and fatal for a sidecar.
- `vis` and `light` do not write a new file. They save the BSP back over the one
  they were given, and `light` drops a `.lit` beside it.
- neither `-threads` on `qbsp` nor `-fast` on `light` exists in 0.18.1, which is
  what a profile nobody had run looks like.

Two shapes were available for running the stages. Either the pipeline becomes a
thing the executor knows about — a job with several processes — or it stays
outside and composes jobs.

## Decision

**A pipeline is several jobs, and `internal/build` only orders and wires them.**

Every process a build starts is a `job.Request` submitted to the same
`job.Service` that `companion job run` uses: same queue, same authorization,
same staging, same containment, same bounded logs, same record. `internal/build`
has no `exec` of its own and no way to acquire one — it depends on `internal/job`
and `internal/profile`, and neither of them depends on it.

The stages therefore **do not share a directory**. Each gets its own job
workspace, which is created and destroyed by the executor, and the files move
between stages through a *build directory* the runner owns. That directory is a
new root role, `build_root`, so the executor is told about it and its containment
rules apply to the files a later stage reads.

Three format members were added, at `aucom.profile/1.1`, because the
alternative was for a profile to encode where the executor stages things:

- `stage_with` on an input — stage this file in the same directory as that one;
- `in_place` on an output — this output is an input the tool rewrote;
- `extension` beside `in_place` — the companion file the tool wrote next to it.

A build writes a **manifest**: the pipeline document's digest, each stage's exact
argv, the SHA-256 of every executable that ran, of every input and of every
output, every diagnostic, and a *recipe key* over the subset that determines the
result. The manifest is written before the first stage runs and refreshed after
each one.

## Consequences

### Positive

- Every guarantee ADR-0003 makes holds for a build, without being restated. A
  stage can be cancelled, retried, inspected and read back by `companion job`
  because it *is* a job.
- A failed build keeps what the failed stage wrote. A leaked map publishes its
  point file — the one artifact that says where the hole is — and publishes no
  BSP, and the stages that never ran say so rather than being absent.
- The preview claim became checkable. Each stage is previewed through the
  executor with the identical request immediately before it is submitted, and
  the two argvs are compared after substituting the job directory a preview gets
  for the one a run gets. A difference fails the build. It is no longer a
  property of the code that somebody has to believe.
- The three format members describe the tools rather than the executor. A
  profile says "vis reads a sidecar"; where the sidecar ends up stays the
  executor's business.

### Negative

- Three jobs instead of one: three workspaces, three records, three copies of a
  BSP that gets rewritten twice. For a map compile, where a stage is measured in
  minutes, that is noise; for a pipeline of very short stages it would not be.
- A pipeline cannot express anything a job cannot. No fan-out, no conditional
  stage, no loop — a step is optional or it is not.
- `aucom.profile/1.1` is a format bump. 1.0 documents are still read; a 1.1
  document read by an older build is refused by name.

### Risks

- **`build_root` widens what a job's inputs may come from.** It is a root like
  any other, so a file inside it is stageable, and the runner is what decides
  what goes in there. The runner copies the user's own inputs in and each
  stage's declared outputs; nothing else is ever written there.
- **A second provider of one capability is refused, not ranked.** That is the
  right answer — picking one would decide which compiler built somebody's map by
  a rule nobody told them — but it means installing a second Q1 toolchain breaks
  every Q1 pipeline until one is removed. It is why the unqualified sample was
  retired rather than kept.

## Alternatives considered

**A job with several processes.** One record, one workspace, no copying between
stages, and a cancellation that stops the whole thing without the runner
noticing. Rejected: it moves ordering and wiring *into* the executor, where
every one of ADR-0003's guarantees would then have to be restated per process,
and it makes "what did this job run" a question with several answers.

**A pipeline that resolves to a shell script.** Rejected on ADR-0001's terms: a
script that arrives from another person is a program you are being asked to run,
and it is the whole thing the profile format exists not to be.

**Output paths that encode the staging layout.** `vis`'s output could have been
declared at `input/bsp/{option.basename}.bsp`, with no format change at all.
Rejected: it makes a portable document depend on one executor's directory
naming, and it is wrong whenever the basename the user passed and the option
they set disagree — which fails as "the action declared outputs it did not
produce" while the tool sits on disk having succeeded.

**A recipe key that covers the outputs.** Rejected on measurement, not on
principle: see below.

## Evidence

Everything below was measured against the pinned `ericw-tools v0.18.1` Linux
build, downloaded through the signed catalogue and run through the Companion.

> **Note, 2026-09-23 ([ADR-0008](0008-the-companion-downloads-no-program.md)):** the Companion no longer downloads
> anything; the same build is now one the user installs and points a profile at.

- **`light` is not bit-reproducible above one thread.** Three runs of one map at
  `-threads 4` produced three different BSPs and three different `.lit` files;
  `-threads 1` produced the same pair three times, and the digest matched the one
  the tool produced when driven by hand. `qbsp` and `vis` are byte-identical
  either way. `auto-pigeon-tools` measured the same thing independently on
  `20260901` for its own acceptance suite. This is why the recipe key covers the
  recipe and not the outputs: a key over the outputs would report every ordinary
  build as irreproducible, which is a true statement about a thread pool and a
  useless one about the build.
- **The failure paths.** A leaking map fails at `compile` under `-leaktest`,
  exits 1, publishes the `.pts` and the log, publishes no BSP, and skips the
  remaining stages. A map whose brushes are wound inward produces
  `*** WARNING 09: Couldn't create brush faces` six times and a segmentation
  fault, and publishes no BSP. A map with no WAD builds — `qbsp` says so — and
  the `--strict` flag turns the same finding into a failed build naming the stage
  and the rule.
- **The sidecar.** `internal/build`'s fixtures reproduce the real behaviour, and
  `TestWithoutStageWithTheSidecarIsSomewhereTheToolDoesNotLook` deletes the one
  member from the same document and watches vis fail with the real tool's
  message.
- **Cross-stack.** `auto-pigeon-tools`' `scripts/aucom-q1.sh gate` builds this
  repository's four fixtures through a freshly compiled Companion, reads the BSP
  with its own BSP29 reader, and checks that the archive the catalogue would
  download is the archive it pinned as its oracle. It is.

## Follow-up work

- A tool profile may declare a `version_probe`, and nothing runs one. The
  manifest answers "which version ran" with the executable's digest and the
  catalogue package it was installed from, which is stronger evidence than a
  banner string (build manifests no longer record the catalogue package — ADR-0008) — but a probe that never runs is a declared feature that does
  nothing, and running it means either a second execution path or a way to
  express a probe as an action.
- There is no HTTP or GUI surface for builds. `companion build` is the whole of
  it.
- A build directory is never collected; nothing prunes `builds/`. (`companion acquire gc`
  and the tool cache it pruned were removed by ADR-0008.)
