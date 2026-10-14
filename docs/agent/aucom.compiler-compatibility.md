---
id: aucom.compiler-compatibility
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: A program is asked what it is, and a profile decides only for itself
authority:
  - aucom-compatibility-probe
  - aucom-compatibility-evidence
  - aucom-compatibility-gate
topics:
  - compatibility
  - compiler
  - probe
  - version
  - ericw
  - qbsp
  - vis
  - light
  - logfile
  - readiness
  - binding
  - rebind
paths:
  - internal/compat/**
  - internal/job/compat.go
  - internal/web/compat.go
  - internal/binding/fact.go
  - internal/profile/compatprobe.go
  - internal/cli/toolchain_check_cmd.go
  - internal/profile/builtin/ericw-tools-q1.tool.json
  - internal/profile/builtin/ericw-tools-auto-pigeon-q1.tool.json
prerequisites:
---

# A program is asked what it is, and a profile decides only for itself

`NEW_323D`. The built-in ericw-tools 2.0 profile passes `-logfile` to vis and light; its own "find
them on PATH" route says PATH usually holds 0.18.1; and a binding to 0.18.1 was called **ready** by
Profiles and by every pipeline list, because readiness asked whether the profile was approved,
whether a file was there and whether the folders were — and never what the file was. qbsp 0.18.1
then compiled the map and vis 0.18.1 refused the option (`Unknown option "-logfile"`). That is the
defect; this module is what must stay true so it does not come back.

## The four rules

1. **The requirement is the document's own, per executable.** `compatibility_probe` on a tool
   profile (`internal/profile/compatprobe.go`, schema `tool-profile-1.1`) names which executables
   are held to it, the literal identities it accepts (`prefix` / `contains`) and the literal text
   each one's own output must show (`mentions`). A document that declares none requires nothing —
   a community profile written for 0.18.1 runs exactly as before. An executable the probe names
   and no action runs is not asked (`compat.ProbedExecutables`): a probe is never the only thing
   that starts a program. Never hold a profile to another
   profile's contract, and never infer vis from qbsp: a qbsp banner says nothing about the vis
   beside it.
2. **An identity is matched, never ordered.** `2.0.0-alpha11-19-g1faae828+auto-pigeon.1.4505`,
   `2.0.0-alpha11` and release number `1.4505` are different identities. Nothing here parses a
   version or compares two; `profile.Version` is for a document's own version and is not used.
   **A document may also NAME identities it must not be run with** (`refuse`: `prefix` /
   `contains` / `suffix` and a one-sentence `reason`, `NEW_323E`). It is the same kind of literal
   statement, by the same document, about a release that was MEASURED: the built-in Auto-Pigeon
   Quake 1 profile names `+auto-pigeon.1.4502` and `+auto-pigeon.1.4505`, which put solid in a
   map's air with exit 0. It is never "this and everything older", it applies only to the profile
   that says it (the upstream profile accepts the same files), and the reason is what the page,
   the CLI and the refused job print. `refuse` is part of the requirement digest only when it is
   not empty, so a document that names nothing keeps every answer recorded for it — pinned by
   digest in `internal/compat`. Do not add an entry without the measurement, and do not use it to
   retire a version merely because a newer one exists.
3. **Asking is a supervised job; the answer is evidence about one file.** A probe is a profile
   action (`compatibility-probe.<executable>`, synthesized by `ToolProfile.ActionByID`, absent from
   `ActionList`) run by the one executor — ADR-0003 names "a version probe" — so it has the argument
   array, the empty stdin, the bounded capture, the time bound, the process-group kill and the
   record every job has. Its exit status is not the answer: 0.18.1 and the Auto-Pigeon build exit 1
   after their usage and upstream alpha11 exits 0. The answer is a `binding.ExecutableFact`
   (binding schema `1.4`), believed only for the same path, size and modification time on a page,
   the same **SHA-256** in the executor, and the same requirement digest. It never writes `Grant`.
4. **One decision, read everywhere, enforced in the executor.** `compat.Judge` is the decision.
   `engine.Checker` reads it for Profiles and every pipeline list; `job.Service.Submit` (and the
   worker, again, when a queued job starts) refuses before a process exists; `build.Runner.Run`
   asks about every mandatory stage before the first. A page poll starts nothing.

## The states, and why they are not one state

| recorded state | fault a page sees | failure class of a refused job | means |
| --- | --- | --- | --- |
| `compatible` | — | — | named itself, accepted, shows everything required |
| `incompatible` | `tool_incompatible` | `tool_incompatible` | named itself and is not what the document was written for — the only PROOF |
| `unknown_output`, `timed_out` | `tool_unverified` | `tool_unverified` | nothing was learned |
| *(nothing describes the file)* | `tool_unverified` | probed on the spot | never asked, or replaced/rebound since |
| `not_permitted`, `wrong_architecture`, `failed` | `tool_unusable` | `tool_unavailable` | the file cannot be run here |
| `missing` | `missing_engine` (as before) | `tool_unavailable` (as before) | not recorded as evidence |

Telling a user their compiler is incompatible when a probe timed out sends them to replace
something never shown to be wrong. Keep the states apart in code, in the page and in the tests.

## When a program is asked

When a path is recorded (`POST /api/v1/profiles/{id}/bind`), when the profile is approved, when a
person presses **Check these programs** (`POST /api/v1/profiles/{id}/check`,
`companion toolchain check <id>`), once at start-up for bindings whose answers no longer describe
their files, and by the executor when it is about to start a program it has no answer for. **Never
on a page poll**, and never for a profile nothing has approved — a probe starts a program with
arguments a document chose.

## What must not be done

- No fallback: an incompatible binding is refused and stays selected and visible; no other
  compiler, profile or path is chosen, and `-logfile` is not dropped to make an old vis run.
  The same holds one level up (`NEW_323E`): Build & Run keeps the PIPELINE the user chose
  selected when it loses readiness (`executionChoices(..., retain)` in `core.js`, the remembered
  choice in `build.js`). Emptying the list let the next refresh select the first ready pipeline —
  a different pipeline behind the user's choice. `testdata/pipelinechoice.check.mjs` holds it.
- No approval from a probe, and no probe result clearing one.
- No requirement on an executable that was not measured. `bspinfo` and `bsputil` are deliberately
  not in the built-in probes. The two Quake II profiles declare one since `Q2_008`, measured on
  0.18.1, 2.0.0-alpha7, alpha11 and fork 1.4505/1.4518 (`TestTheBuiltInQuake2ProfilesJudgeTheInstalledReleases`):
  alpha7's qbsp stops its usage (`number is too big`) before `-basedir`, so the upstream document
  holds qbsp only to `-q2bsp` and `-leaktest`. The fork's Quake II document does not refuse 1.4502
  or 1.4505: that defect was measured on Quake 1 maps only.
- Compatibility is not correctness. A compatible fork says nothing about what its qbsp does to a
  non-planar pipe (`NEW_324A`); that is the compiler's own acceptance.

## A folder is asked what it holds, and a token never repeats a switch (`Q2_008`)

- **`holds_any` on a root** (profile-common 1.1, additive) is a document's literal statement of
  what a bound folder must hold — relative paths, at least one present. It exists for the folder a
  program does not complain about: Quake II's qbsp handed the folder above `baseq2` exits 0 with
  its built-in palette and no textures. `engine.Checker` (Profiles, Build & Run, `build
  pipelines`) and `profile.resolveRoots` (the executor and every preview) ask it, and a folder that
  holds none of them is `missing_game_data`, refused by name.
- **A recorded folder that is the wrong folder makes the installation need setup**, like a
  recorded program that is the wrong program (`installReadiness`): a tool whose `inspect` action
  reads no folder is not "ready" while every action that compiles cannot start. An UNBOUND folder
  stays per action.
- **A build tool's folder is never fixed with `engine bind`**, which refuses a tool's id:
  `rootFix`, beside `bindFix`, names Profiles and `acquire resolve … --root role=path --bind`.
- **A person's own token never repeats a switch the action writes** (`profile.ManagedSwitchConflict`):
  `-threads` on a stage whose action passes `-threads {option.threads}` is refused where it is saved
  (stage-arguments API, `toolchain args`) and when a command is resolved, naming the parameter or
  folder that sets it. A program handed a switch twice honours one of them, and which one is its
  choice. A switch the action never passes is the person's own.

## The pinned release, and where its facts come from

The two Auto-Pigeon-build profiles name ONE release of the fork, and
`internal/profile/builtin/testdata/auto-pigeon-ericw-release-manifest.json` is that release's own
published `release-manifest.json`, byte for byte. `forkrelease_test.go` fails unless the profiles'
name, tag, banner and every published archive's name and SHA-256 are the manifest's. To move the
pin: replace the fixture with the new release's manifest and edit the two documents until the test
passes — never the other way round, and never from a banner sorted by eye. A release is pinned
only after the fork's workflow accepted its archives; a compatible program is still not a correct
compiler, and what a release was measured NOT to do goes in the profile's description.

The binding store is `aucom.local-binding/1.4`. A 1.3 store is read unchanged and stamped 1.4 on
the first write (`internal/binding/upgrade_test.go`); a build that reads only up to 1.3 refuses a
1.4 file whole. Never write a downgrade and never promise a rollback without the user's own copy.

## Tests that hold it

`internal/compat` (classifier on the measured banners, requirement digests, one sentence per
state); `internal/job/compat_test.go` (shell fixtures that record their own argv — "never
invoked" is read from what the program wrote); `internal/job/compat_installed_test.go` (the real
0.18.1, alpha11 and fork, named by `AUCOM_TEST_ERICW_LEGACY`, `_ALPHA11`, `_FORK`, and `_FORK_DEFECTIVE` for 1.4502 / 1.4505);
`internal/engine/compat_test.go`; `internal/web/compat_test.go` and
`internal/cli/toolchain_check_cmd_test.go` (page, API and CLI give one answer — including the
`build pipelines` LISTING, which asked only whether a profile provides each capability and printed
"ready" for a pipeline bound to the wrong programs until the live journey of `NEW_323D` read the
two side by side; it now asks the same `engine.Checker` per mandatory step).
