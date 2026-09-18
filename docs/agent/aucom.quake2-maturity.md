---
id: aucom.quake2-maturity
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: Quake II is work in progress, and no document may say otherwise
authority:
  - aucom-quake2-maturity
topics:
  - quake2
  - q2
  - maturity
  - work-in-progress
  - engine-family
  - capability
  - compiler
  - toolchain
paths:
  - internal/maturity/**
---

# Quake II is work in progress, and no document may say otherwise

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 230-287 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

## 3. QUAKE II IS WORK IN PROGRESS, AND NO DOCUMENT MAY SAY OTHERWISE (`AUP/AUCOM 215`)

`20260906_215` added a Quake II path that works and is not finished, and the
second half of that sentence is the part with machinery behind it.

**The statement lives in `internal/maturity`, keyed on AUB's `engine_family`.**
It is not a member of a profile document, and it must not become one. A profile
is written by whoever publishes it; the first community Quake II toolchain to
declare itself stable would be a community document switching off a warning this
build stands behind. `maturity.Of(family)` is what *this build* says, a
community document is subject to it exactly as a built-in one is, and every
surface — CLI listings, `profile show`, `build preview`, a running build, the
Build/Run/Profiles areas, AUP's own panes — renders the same
`maturity.Quake2Message`. Its SHA-256 is pinned here and in AUP's
`frontend/src/services/gameMaturity.ts`, the way the portability corpus is,
because neither repository can read the other at test time.

**The two toolchains have different capability ids on purpose.** `q1.bsp.compile`
and `q2.bsp.compile`. A shared `bsp.compile` would make "which compiler runs
this step" a question a pipeline answers by iteration order, and the wrong
answer produces a Quake 1 BSP for a Quake II project.
`TestAQuake2PipelineCannotResolveAgainstTheQuake1Toolchain` checks both
directions. Never merge them.

**Everything the Q2 documents claim about ericw-tools 2.x was measured**, by
running 2.0.0-alpha7 against a synthetic Quake II map. Seven behaviours differ
from the qualified Q1 line and each is written into the document with its
reason: no `bin/` in the archive, `maputil`, `<stem>-vis.log` / `<stem>-light.log`
written *beside the input*, the `<stem>.texinfo.json` that `light` reads back,
`-lit` doing nothing, `bspinfo` writing files, and contents coming from the
`.wal` rather than from the texture name. A future change that re-derives a Q2
document from the Q1 one is undoing that.

**The base game data root is required, and that follows from the last of those.**
A Quake II compile with nothing bound at `game_root` exits 0 and writes a BSP
with default flags, no playerclip and no sky. Making the binding optional would
trade a refusal that names the binding for a map that looks built and is wrong.
Three roots, bound separately: `tool_root`, `game_root` (`-basedir`),
`content_root` (`-gamedir`).

**An engine profile declares the actions upstream documents and no others.**
FTEQW's Quake II profile has `join_server` alone, because upstream's own
QuickStart says every local Quake II server needs gamecode FTEQW does not ship
and Auto-Pigeon must not distribute. Yamagi uses `-datadir` and `+set game`, not
`-basedir` and `-game`, because Yamagi's own filesystem source calls the latter
deprecated. Adding an action to make a button appear is the failure mode here.

**`internal/feedback` builds a report; it does not filter one.** There is no
member of `feedback.Report` that can hold a map, a log or a path, so nothing has
to be cleaned up. `Consent` is four booleans defaulting to none, and the report
records what was chosen so "did not share" and "shared, and there was none" stay
distinguishable. A diagnostic carries the *profile's declared* message, never
the tool's line. A credential or a path is **refused and named**, using
`profile.CheckPortable` and `job.Redactor` as detectors rather than as filters —
quietly redacting would hand a user a document they believe they wrote. Nothing
in this repository sends a report anywhere, and adding a destination is a
decision about somebody else's data, not a convenience.
