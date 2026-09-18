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
paths:
  - internal/maturity/**
  - internal/q3deps/**
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
Upstream's Linux release is a `.7z` holding one AppImage of the whole NetRadiant
editor; Windows is a 43 MB zip of the same; macOS has nothing; and `q3map2`
resolves libassimp, libdraco, libminizip and libicu out of the bundle's own
`../lib`, so there is no smaller artifact to prefer. `internal/acquire` unpacks
zip and tar.gz. A prompt that adds a `managed_download` here has to add a
catalogue entry for a map editor first, and `TestQ3Map2DeclaresNoManagedDownloadAndSaysWhy`
is where it is asked to think about that.

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
3. **The limits are a member of the report**, not a paragraph in a README. A
   model's internal references are not read, and the report says so where the
   model is listed. Do not remove a limit sentence; add one when you add a gap.
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
