---
id: aucom.leak-adapters
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: A leak test is one adapter per game, measured before it is listed
authority:
  - aucom-leak-adapter-table
  - aucom-leak-profile-authority
  - aucom-leak-verdict-evidence
  - aucom-leak-next-game
topics:
  - leak
  - leaktest
  - leak-test
  - pointfile
  - lin
  - pts
  - q3map2
  - qbsp
  - adapter
  - next-game
  - verdict
paths:
  - internal/leakadapter/**
  - internal/web/leaktest.go
  - internal/web/leakpipeline.go
  - internal/web/leakrelay.go
  - internal/aub/leaklink.go
  - internal/profile/builtin/q1-leak-test.pipeline.json
  - internal/profile/builtin/q3-leak-test.pipeline.json
prerequisites:
---

# A leak test is one adapter per game, measured before it is listed

`Q3_018` turned the editor's Leaks → Test in Companion flow from a Quake 1 feature into a table
with two rows. This module is the contract a third row has to meet, and the note a next-game
template (`Q3_015`) links to rather than restates.

## The table, and what decides a row

`internal/leakadapter` is the only place that says which compiler answers "does this saved map
leak" for which game. The editor (`frontend/src/domain/leakPointfile.ts`) and the account's server
(`internal/companion/leakresult.go`) hold the same rows, because each of them refuses a claim that
is not one.

| game | built-in pipeline | compiler | point file | direction | envelope |
| --- | --- | --- | --- | --- | --- |
| `quake1` | `auto-pigeon.q1.leak-test` | `ericw-qbsp` (EricW 2.0.0-alpha11; 0.18.1 also qualified) | `ericw-pts` | `occupant_to_outside` | `aucom.leak-result/1.0` |
| `quake3` | `auto-pigeon.q3.leak-test` | `q3map2` (2.5.17n-git-68ecbed) | `q3map2-lin` | `outside_to_occupant` | `aucom.leak-result/1.1` |

The pipeline column is the built-in one. It is not what runs unless the user chose it — see the
next section.

**A game with no row is unsupported, by name. It is never Quake 1.** Quake II has a `qbsp`; that
is not a measurement, and it has no row.

**The game is the saved document's own word**: the `game` field of the pinned, digest-checked
APMap. The review reads it from the asset cache's verified bytes (`savedMapGame`); the build start
reads it again from the bytes it just staged (`leakBindingForBuild`); the result builder refuses a
build whose conversion record names another game than its pipeline's. A link's `profile=` hint, the
pipeline a page selected and the state of any toolbar are checked against it and never used instead
of it. `build.CheckConvertedGames` applies the same rule to every pipeline: a map of one game is
not handed to another game's compiler.

## Which pipeline answers: the user's pin (HITL, 2026-10-06)

The Companion ships profiles; the compilers are programs the user installs. So **which pipeline
answers a game's leak test is the user's choice, per game, with no default**: `leak_test_pipelines`
in config.json, `GET/POST /api/v1/leak-test/pipelines`, and a chooser the Companion opens when a
request arrives for a game with nothing pinned (eligible pipelines, built-in first, unready ones
disabled with their reasons, a way to Profiles). There is no "first ready" fallback and no automatic
pin; Cancel starts nothing and keeps the request; a pin that is gone or unusable is reported and
asked for again, never replaced by another.

A pipeline is ELIGIBLE when `leakadapter.Adapter.Bind` binds it: it is for that game and publishes,
by ROLE, the point file and the compiler's text (a role-named log, or the reserved
`aucom.step.stdout` of the step producing the point file). A friendly name or an output's file name
is not evidence. The binding — game, pipeline, point-file/log/BSP output names and the compile step
— is recorded in the build manifest (schema 1.4, `leak_test`), and the result is read from THAT
record, never from today's pin or a fixed step id.

## What the pipeline owns

- The built-in leak test is one step, the compiler's structural/BSP stage with its leak test on.
  A user's pinned pipeline may have more stages; its leak stops at the bound compile stage, and the
  later stages are skipped, never run on a BSP that was not written.
- The compiler's own text is the reserved step output `<step>.stdout` (role `aucom.step.stdout`):
  the job's stored log, written once when the process ends — success, failure, timeout or cancel —
  and never the live buffer a page polls. A compiler that writes a log file of its own (qbsp)
  declares it instead.
- Every output is collected from the job's own fresh workspace and published inside the build.
  Nothing is ever looked for by pattern, and nothing is written beside the user's source. The
  result builder checks the point file's digest, that it sits inside the build, and that it is
  named after the source the build staged.
- The compile action's BSP stays REQUIRED. A leaked test is therefore a failed step — the truthful
  state of a compile that wrote no BSP — and ordinary builds are not relaxed to make it succeed.

## Three states that are never one

| state | where | a leaked Quake III test |
| --- | --- | --- |
| process exit | `compile_exit_code` | `0` |
| pipeline state | `build_state` | `failed` |
| leak outcome | `diagnostic.outcome` | `leak` |

Delivery to the editor is a fourth, kept beside the build in `leak-return.json`; a failed delivery
is retried from there and compiles nothing.

## Reading a run: the evidence, in order

`ClassifyQ3Map2` is the reference. Its precedence is the contract:

1. A text that is not this compiler's log is no verdict.
2. The compiler SAYING it reached an entity from outside is a `leak`, whatever the exit status. A
   fresh, well-formed point file under the leak banner says the same. A leak without a usable
   point file is still a leak, without a route.
3. An error the compiler stopped on, a cancelled step or a log cut short is `incomplete`.
4. The leak banner with nothing reached is an empty flood — `no_interior`, "not tested". Measured:
   with no entity in open space, or every entity inside a brush, Q3Map2 prints the same `leaked`
   banner, names no entity and writes no `.lin`.
5. `no_leak` needs everything: the fill ran, the BSP was written AND is there, the footer was
   printed, exit 0, the step succeeded, and the version is the measured one.

Never: a missing point file read as a pass; exit 0 read as a pass; a BSP read as a pass; any
`severity: error` diagnostic read as a leak. Qualifications travel with the verdict
(`shader_image_missing`, `version_unqualified`); a shader the compiler could not find is reported,
not replaced with a guess about whether it is solid.

What "sealed" means is the compiler's, and is measured per game: for Q3Map2 a curved patch, a
detail brush and a nonsolid shader do not seal a gap.

## Adding a game

A row is a claim that somebody did all of this, on the machine, with the installed compiler:

1. Authored controls — sealed, a wall missing, an entity outside, no entity, every entity in solid,
   the gap covered by whatever that game has that is not structural, an unreadable map — compiled
   DIRECTLY, with exit status, files left and decisive lines written down
   (`internal/leakadapter/testdata/q3/controls` and its `gen_controls.py` are the pattern).
2. The compiler's point file format and DIRECTION established from those runs, not assumed.
3. A classifier with the precedence above, tested against the recorded output, in both Companion
   and the editor, from the same vectors.
4. A dedicated one-step pipeline, a row in each of the three tables, and — when the envelope needs
   a field it does not have — a new envelope version that the old readers refuse.
5. The live journey: saved revision → link → notice → Review → Build → return → route, on the real
   desktop.

Until then the game is unsupported, and says so.
