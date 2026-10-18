---
id: aucom.quake2-maturity
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: Quake II is work in progress, and no document may say otherwise
authority:
  - aucom-quake2-maturity
topics:
  - quake2
  - yamagi
  - pak
  - q2
  - maturity
  - work-in-progress
  - engine-family
  - capability
  - compiler
  - toolchain
paths:
  - internal/maturity/**
  - internal/q2deps/**
  - internal/q2pack/**
  - internal/q2install/**
  - internal/q2run/**
  - internal/cli/package_map_q2.go
  - internal/web/q2package.go
  - internal/web/playq2.go
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

**Measured with release 1.4518 (`Q2_008`), and held by the documents:** `-basedir`
is the folder that HOLDS `pak0.pak` — `baseq2` itself; the folder above it compiles
exit 0 with `using built-in palette` and no textures, so `game_root` is held to
`pak0.pak` or `pics/colormap.pcx` (`holds_any`). light without the compile's
`<stem>.texinfo.json` also exits 0 — a `func_group` with `_minlight` lit 504
bytes differently — so `texinfo` is a REQUIRED input of light and a compile that
left none stops the build at light, as `output_missing`. qbsp 1.4518 accepts
`-threads`; no Quake II action passes it. Compile and VIS are byte-deterministic;
LIGHT with more than one thread is not, so two four-thread builds of one map are
compared by stage, not by the final BSP's hash.

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

## A Quake II map is played from a PAK the engine says it opened (`Q2_009`)

`companion package map` and the Build page's *Package and run this map* panel
take a finished Quake II build through `internal/q2pack` (what goes in),
`internal/q2install` (where it goes) and `internal/q2run` (whether the engine
loaded THAT map). They decide nothing themselves. Each rule below was measured
with Yamagi Quake II 8.70 built from tag `QUAKE2_8_70`, retail data, ericw-tools
1.4518, on a real display, and each would be undone by a change that looked like
a convenience.

- **What the engine asks for is read from the BSP** (`internal/q2deps`): every
  texinfo's `textures/<n>.wal`, the sky's six faces, `noise`, `model` and a
  `.md2`'s own skins. A sky face is carried in every format the content holds:
  Yamagi's paletted renderer asks for `.pcx`, its GL renderers for `.tga` only —
  a package with only the first draws no sky in GL (found live).
- **A reference is found where the engine's own `FS_FOpenFile` finds it**
  (read in Yamagi 8.70's `src/common/filesystem.c`, `shared.c`): within one
  folder the archives first — not-numbered `.pak`s, then `pak<N>` from the
  highest — matched ignoring ASCII case; then the loose file under the name as
  asked, then the name with only `A`-`Z` lower-cased (`Q_strlwr`, "C" locale).
  Each loose try is an open, so whether another spelling opens a file is the
  host filesystem's answer, measured per lookup and matched to the indexed file
  by identity; on a case-insensitive one `Mine/` and `mine/` are one folder
  (reproduced on a real exFAT volume, Q2_015A). `Location.Path` is always the
  spelling on disk. Tests measure the fixture folder's case behaviour with a
  sentinel, never `GOOS`, and also run the resolver through `foldingStat`, the
  case-insensitive simulation, so a Linux runner checks the macOS/Windows path.
- **Nothing of the base game is packaged, nothing of the user's without a
  grant**, and what nothing supplies is named before an archive is written. A
  map name the base game or the content folder already has under `maps/` is
  refused (the downloaded original of an edited corpus map). There is one PAK
  writer, `internal/pack`; its sidecar is rewritten without local paths.
- **The folders a build read come from the build**: request roots first, then
  the compiling tool's `ToolRecord.roots` (the binding's). A build from the
  Build page names none in its request (found live).
- **A package goes into a mod folder of its own beside `baseq2`**, staged as a
  hidden generation, re-hashed and read back, then renamed into place; the one
  it replaces is put back on failure, cancel leaves it as it was, installs into
  one game folder are serialised by a lock in the Companion's state. `baseq2`,
  `xatrix` and `ctf` are reserved in `engine.CheckModName` for every staging
  path (they were not before).
- **A run is accepted only on the engine's own word for the file.** Yamagi's
  `play_map` passes `+set fs_debug 1`; `q2run` accepts `Map: <name>` only after
  `FS_FOpenFile: 'maps/<name>.bsp' (found in '<the installed .pak>')`. Measured:
  a loose map in a folder the engine searches first (the mod's copy beside the
  program, or in its settings folder) is loaded silently in place of the
  package — that is `load_order_shadowed`. `fs_debug` is also what keeps a
  quiet engine's output flowing: Yamagi writes its console with stdio, which is
  block-buffered on a pipe.
- **Every Yamagi action passes `-cfgdir YamagiQ2`.** A job's `HOME` is its own;
  without it Yamagi wants `$HOME/.local/share/YamagiQ2/`, creates only the last
  level and dies at start (every Companion launch, before `Q2_009`).
- **Setup asks what the engine loads, without starting it** (`engine/native.go`):
  the game library and a renderer, by the profile's `runtime`, and the program's
  header for the computer it was built for. The game folder must hold
  `baseq2/pak0.pak` or `baseq2/pics/colormap.pcx` (`holds_any`): a `baseq2`
  holding no game stops the client at once.

**Build & Run is a third entry to the same three owners (`Q2_020`).** Q2_009
refused a Quake II build profile there; now `playrun.Request.Family == quake2`
makes the sequence a *packaged* one (`internal/web/playq2.go`): no texture-bundle
stage (the compiler reads its bound game folders, so the build is handed no
`content_root` of its own), Installing = `q2pack.Prepare` → `Store.Create` →
`q2install.Install` with the review's grants, Launching = `q2run.Launch`
registered in the panel's run registry, so the panel's guards and Stop see it. A
held plan fails at Installing with the plan's words before anything is written;
an engine refusal fails at Launching and keeps the complete installation. Only a
Quake II engine and a client map action fit a Quake II build profile there.

The engine-output watcher and wait (`internal/enginewatch`) are shared with
Quake III's `q3run`; a family adds its own checks through `Inspect`/`Confirm`,
not a second watcher.

None of this changes the maturity statement. Corpus maps that leak under
ericw-tools 2.x (F1) or are refused at parse (F2) are not packaged.

