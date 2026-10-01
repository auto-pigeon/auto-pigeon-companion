---
id: aucom.quake3-maturity
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: Quake III is work in progress too, and it is not Quake II renumbered
authority:
  - aucom-quake3-maturity
  - aucom-q3map2-no-managed-download
topics:
  - quake3
  - q3
  - q3map2
  - maturity
  - work-in-progress
  - shader
  - patch
  - toolchain
  - dependencies
  - staging
  - fs_game
  - packages
  - failure-class
paths:
  - internal/maturity/**
  - internal/q3deps/**
  - internal/q3vfs/**
  - internal/q3packages/**
  - internal/artifactcheck/**
  - internal/failure/**
prerequisites:
  - aucom.quake2-maturity
---

# Quake III is work in progress too, and it is not Quake II renumbered

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 288-368 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

## 4. QUAKE III IS WORK IN PROGRESS TOO, AND IT IS NOT QUAKE II RENUMBERED (`AUP/AUCOM 216`)

`20260906_216` added a Quake III path on the pattern `215` established, and the
places where it is deliberately NOT the same pattern are the ones that bind a
future prompt.

**Its sentence is its own.** `maturity.Quake3Message` names shader, patch and
entity workflows because a Quake III map has a shader script and patches; the
Quake II sentence does not, because a Quake II map has neither. Both are pinned
here and in AUP's `frontend/src/services/gameMaturity.ts`, and
`TestTheTwoSentencesSayDifferentThings` fails a change that derives one from the
other. Adding a family is one row in one table on each side — every surface
already reads it, and a surface that had to be told about a new game is a
surface that would one day not be told.

**There is no managed download for Q3Map2, and that is the decision, not a gap.**
Since 2026-09-23 there is no managed download for anything — the Companion
downloads no program, and a `managed_download` route is legacy, read and refused
(`acquire.ErrNoDownloads`). The Q3-specific reason still stands and still
matters to a prompt that wants to point users at an artifact: upstream's Linux
release is a `.7z` holding one AppImage of the whole NetRadiant editor; Windows
is a 43 MB zip of the same; macOS has nothing; and `q3map2` resolves libassimp,
libdraco, libminizip and libicu out of the bundle's own `../lib`, so there is no
smaller artifact to prefer. Users install it themselves and point the profile at
it (`user_path`, `system_path`). `TestQ3Map2DeclaresNoManagedDownloadAndSaysWhy`
still fails a document that declares one.

**Q3Map2 is one program with three stage switches**, where the EricW documents
are several programs. Its capability ids are `q3.bsp.compile`, `q3.bsp.vis` and
`q3.bsp.light` — a third set, not a merge — and
`TestAPipelineCannotResolveAgainstAnotherGamesToolchain` now checks six pairs
rather than two, because "it is all the same compiler anyway" is exactly the
reasoning that would produce a Quake 1 BSP for a Quake III project.

**Nine measured behaviours are written into the document, and each differs from
both EricW toolchains.** There is no output argument at all — the BSP, portal
and surface files appear beside the input, so every output is declared with
`in_place` and an extension rather than a path. `-vis` needs the `.prt` and
nothing else; `-light` refuses to start without BOTH `<stem>.srf` and
`<stem>.map`, which is why the pipeline wires the map source into the lighting
step as well as the compile. `-vis` without `-saveprt` deletes the portal file it
was handed, so that switch is not an option. A leak exits 0 having written no
BSP, so it is the missing required output that fails the job. A missing texture
is a warning and exit 0. Lighting is nondeterministic above one thread. A
re-derivation of the Q3 document from the Q1 or Q2 one is undoing all of that.

**`internal/q3deps` reviews; it does not filter, and it does not repair.** It
resolves what a map names against the archive about to be written, the user's
content and the base game, and `package create --map` refuses on it. Four rules
hold it up:

1. **It agrees with the compiler, and that is a test.**
   `testdata/q3map2-2.5.17n-report.txt` is what Q3Map2 actually printed for the
   fixture map; the scan's `missing` set has to equal the shaders it could not
   find an image for. A change to the parser that stops agreeing fails there.
2. **The base game's shader scripts are read, out of `pak0.pk3` as well as
   loose.** A review that called every `common/*` shader missing is a review a
   user learns to click past — and one nobody reads is worse than none. What a
   base-game shader pulls in is NOT followed: it is inside somebody else's PK3.
3. **The limits are a member of the report**, not a paragraph in a README. An
   `.md3` model's own shader names ARE read since `Q3_011`; any other model
   format's are not, and the report says so where that model is listed. Do not
   remove a limit sentence; add one when you add a gap.
4. **`--accept-missing` requires `--reason`, prints the review first and prints
   the reason back.** It is not a way to switch the check off, and there must
   never be a flag that skips the review itself.

**A face or patch shader name carries no `textures/` prefix** — measured both
ways, including for `patchDef2`. The scan normalizes the way the compiler does
and names the doubling when it sees it, because `textures/textures/…` reads like
a missing file and is an authoring mistake.

**ioquake3 declares all five actions** where FTEQW's Quake II profile declares
one, and the difference is upstream's own download: `baseq3/vm/qagame.qvm` ships
beside the engine, so a local Quake III server needs no third-party gamecode.
What it still needs is `pak0.pk3`, which is id's. The generic id Tech 3 profile
sends only `fs_basepath`, `fs_game`, `sv_maxclients`, `map` and `connect`, and
has no dedicated-server action because the name of a dedicated binary is each
project's own invention.

**The compiler redirects `-fs_homepath` and the engines do not.** A build must
read only what was bound to it; a player's settings, demos and screenshots
belong where the engine puts them. Those are different requirements and must not
be made consistent with each other.

## A Quake III build reads what was staged, and a stage is judged by what it produced (`Q3_010`)

These five rules were each bought with a measurement of Q3Map2 2.5.17n, and each
would be undone by a change that looked like a simplification.

**The compiler is never handed a folder the user chose.** `internal/q3vfs`
stages `<root>/baseq3` and `<root>/<fs_game>` of `game_root` and `content_root`
into `<build>/vfs/<role>/`, and `internal/build/gamedata.go` substitutes the
staged directories before any step is submitted — for every caller, because it
is in `Runner.Run`. Measured: `-fs_game ..` made Q3Map2 read the parent of each
base path, a mod directory that is absent is initialised in silence, and a
truncated PK3 is skipped with exit 0. Do not "optimise" the staging away for a
local folder, and do not add a caller that builds a Quake III pipeline around
the runner. `job run` on one action is the documented exception and is not
staged; its `mod` option still refuses `.` and `..`, in `OptionSpec.Check`.

**The mod directory is found in the DOCUMENT, not by a name.** The option that
follows a literal `-fs_game` in an action's args is the one; `fsGameOption`
reads it. A rule keyed on an option called `mod` would stage nothing for a
profile somebody else wrote. Every stage must name the same mod, and a mod no
approved folder has is refused (`fs_game_not_found`).

**A saved map's bound packages are staged by the Companion, by digest.**
`internal/q3packages` reads worldspawn's `auto-pigeon.packages` out of the APMap
— before conversion, because the `.map` cannot carry it — finds each binding in
the account BY ITS SHA-256, never by name or package id, and downloads through
`assetsync.Store.Publish`, which verifies. An unreadable ledger is refused, not
treated as empty. `q3vfs` re-hashes each archive when it stages it. A second
writer of "which packages does this build read" — a folder the user copied the
archive into, a download that trusts the row's own digest — is the defect
`Q3_007` recorded and this closed.

**Exit status is not the verdict.** A profile rule may be `fatal` (the line
proves the result unusable although the exit was 0) and may carry a `class`.
Q3Map2's `ERROR: Unable to open file` is fatal; a leak and a missing image are
classed. `internal/artifactcheck` reads `.bsp`, `.prt`, `.srf`, `.lin` and the
`.map` source as their role, on every staged input and every collected output:
`-light` given an EMPTY `.srf` exits 0 with a lit BSP, which is the case an
existence check passes. A missing image is still a WARNING and the build still
says "not a complete result" (`Q3_006`); do not promote it to fatal, and do not
demote the model line.

**A failure has a class, attached where it is known.** `internal/failure` is a
token beside the sentence, set by the function that knows the cause and read
with `failure.Of`. Never derive a class from an error's text, and never add a
class called "unknown": an unclassified failure is a fact about that failure.

