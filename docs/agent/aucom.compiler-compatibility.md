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
- No approval from a probe, and no probe result clearing one.
- No requirement on an executable that was not measured. `bspinfo` and `bsputil` are deliberately
  not in the two built-in probes, and the Quake II profiles declare none: add one with the
  measurement that justifies it.
- Compatibility is not correctness. A compatible fork says nothing about what its qbsp does to a
  non-planar pipe (`NEW_324A`); that is the compiler's own acceptance.

## Tests that hold it

`internal/compat` (classifier on the measured banners, requirement digests, one sentence per
state); `internal/job/compat_test.go` (shell fixtures that record their own argv — "never
invoked" is read from what the program wrote); `internal/job/compat_installed_test.go` (the real
0.18.1, alpha11 and fork, named by `AUCOM_TEST_ERICW_LEGACY`, `_ALPHA11`, `_FORK`);
`internal/engine/compat_test.go`; `internal/web/compat_test.go` and
`internal/cli/toolchain_check_cmd_test.go` (page, API and CLI give one answer).
