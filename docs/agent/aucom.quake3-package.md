---
id: aucom.quake3-package
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: A Quake III map package carries only what was granted, and a run waits for the engine's own word
authority:
  - aucom-quake3-package-rights
  - aucom-quake3-install-targets
  - aucom-quake3-run-acceptance
topics:
  - quake3
  - pk3
  - package
  - packaging
  - rights
  - licence
  - grant
  - install
  - load-order
  - engine
  - launch
  - map-load
  - base-game
paths:
  - internal/q3pack/**
  - internal/q3install/**
  - internal/q3run/**
  - internal/enginewatch/**
  - internal/q3deps/**
  - internal/cli/package_map_cmd.go
  - internal/web/q3package.go
  - internal/web/assets/q3package.js
prerequisites:
  - aucom.quake3-maturity
---

# A Quake III map package carries only what was granted, and a run waits for the engine's own word

`Q3_011` added the path from a finished Quake III build to a map running in an
engine: `internal/q3pack` (what goes in the archive), `internal/q3install`
(where the archive goes) and `internal/q3run` (whether the engine loaded it).
`companion package map` and the Build page's *Package and run this map* panel
both call those three and decide nothing themselves. The rules below were each
bought with a measurement or a mistake made on the way, and each would be undone
by a change that looked like a convenience.

## What goes in

**Runtime need is read from the BSP, never derived from the map source.**
`q3deps.ParseBSP` reads the shaders drawn surfaces and fog volumes name and the
entity lump's `model2`, `noise` and `music`. The map source says what the
COMPILER looked for. They differ in both directions — a `misc_model` is baked
in, so its `.md3` is compile-only and its skin (which the source never names)
is runtime; `model2` is the reverse — and a rule about names would get one of
them wrong. Measured on Q3Map2 2.5.17n: a shader's `qer_editorimage` absent is a
warning, its stage image absent is silence. So **a compile that exited 0 with no
warning is not evidence a package is complete**, and `q3pack` is where a missing
runtime file stops things.

**Nothing from the user's content is packaged without a grant.** A grant is for
an archive BY DIGEST, for the loose files, or for one path; its basis is
`own_work`, `licensed` (the licence is named, or the grant is refused) or
`not_redistributable`. Never infer one — not from a file's name, not from the
folder it is in, not from a public repository containing it, not from the map
having been bound to the package. The base game (`game_root`) is never packaged.
A file whose bytes are a known released asset (`pack.AssetCorpus`) is not covered
by the grant for its archive or folder: only a grant naming that path is.

**A file an engine needs and the archive will not carry is a refusal**
(`package_held`), acceptable only with `--accept-missing --reason` — rule 4 of
`aucom.quake3-maturity`, unchanged: the review is printed first, the reason is
printed back and recorded, and there must never be a flag that skips the review.
A duplicate path and an unsafe path are NOT acceptable by a reason.

**An archive below the top of a game directory is a file, and is not opened**
(`Q3_012B`). Q3Map2 and every engine load `<game>/*.pk3` and nothing deeper —
measured again with an authored index image: found in a top-level `.pk3`, not
found in `baseq3/tools/x.pk3` — and `q3vfs` stages a deeper one as the loose file
it is. `q3deps` used to open any `.pk3` it walked past, so a shader script inside
a nested tool pack became a dependency "found in" an archive the build never
staged, and `package map` stopped with *Build the map again*, which changes
nothing. The scan now opens only archives directly in a game directory and names
the rest in `limits` as *not loaded by the compiler: not directly in the game
directory*. Do not widen the walk back: a file only a nested archive holds is a
file no program can open, and reporting it found is the incomplete package this
review exists to prevent.

**There is one PK3 writer, `internal/pack`.** `q3pack` plans and calls
`pack.Create`. Do not add a second ZIP writer, do not lower-case or otherwise
respell a member path (a loose file keeps the capitalisation it has on disk, an
archive member the name its archive gives it), and do not add a `--replace`: a
package somebody may already have installed is never written over. Members read
out of somebody's archive go through `pack.Extract`, which is what refuses an
archive with a path that climbs out of it.

## Where it goes

**The default install does not write into the user's game folder.**
`q3install.Managed` builds a base directory the Companion owns: the target game
directory is real and holds the archive plus one link per entry of the user's
own; every other game directory is one link (`joincontent.LinkDir`, which is
the junction fallback on Windows). **Game data is never copied** — a file that
can be neither symlinked nor hard-linked is a refusal, not a copy of `pak0.pk3`.
`q3install.GameFolder` is the explicit target: one file, placed with a hard
link from a temporary name so an existing file is never replaced, recorded with
its digest, and removed only while it is still those bytes.

**Load order is checked before anything is written.** Measured on ioquake3 1.36:
loose beats every archive of its game directory, the LATER archive name wins
without regard to case, the mod beats the base game. `auto-pigeon-…` therefore
loses to `pak0.pk3`. A map something else would supply is refused
(`load_order_shadowed`); do not "fix" that by renaming the archive to sort
last — a name chosen to win is a name that silently overrides the user's game.

**A managed install stages the packages the saved map is bound to; nothing
else does, and nothing ships them** (`Q3_017A`). `Q3_014A` got a textured map in
an engine only by linking the bound PK3 beside the map by hand: the install
placed the map's archive and the game's links and knew nothing of what the
compiler had read from `vfs/content_root`. The identity was already recorded —
`q3pack` copies each staged bound package into the plan as a source with an
`origin` (name, game folder, SHA-256, size) — so `q3install` reads it from the
package's own record, asks `q3packages.Resolve` for the cache file BY DIGEST with
no account (an install fetches nothing), and stages it under the folder and name
the binding gives. The rules that keep that safe:

- **Never from a name.** What is staged is what the record lists with a digest.
  Do not find a package by a texture or archive name, do not scan the cache, and
  do not widen it to "whatever was in `content_root`" without deciding what a
  user's own loose content folder means for an engine first.
- **Verified where it is, then published.** A managed installation is built as a
  `.installing-*` generation beside its place, every staged file is hashed there
  against the build's digest and size, and only then is it renamed into place
  (the one it replaces is moved to `.retired-*` first and put back if the rename
  fails). So a failed or cancelled install leaves the previous installation
  listed and runnable. Do not go back to removing the old one first.
- **Missing is said before launch.** `Preview` refuses an absent package and one
  of the wrong size (`game_data_missing`, `archive_damaged`), `Install` also
  refuses wrong bytes, and `Verify` — which `q3run.Launch` calls — re-hashes
  every staged package. A preview does not hash: that is the install's job.
- **The name is the precedence.** A staged package keeps the binding's name, and
  `Package.searched_before` / `shadowed` / `overrides` say what that means in
  the engine's order, by name. Do not rename one to make it win or lose, and do
  not claim the engine's order equals Q3Map2's: the compiler read bound packages
  from a root of their own.
- **The user's own copy is used under the user's own spelling, and "nothing
  was added" is counted from the directory.** A file of the bound name in the
  game folder — the name compared without regard to case, as the engine does —
  with the bound bytes IS the package (`in_game_folder`): the managed base
  reaches it through its link, spelled as the user spelled it, and nothing is
  staged. Other bytes under that name are `install_conflict`. A test of either
  reads the directory's entries and counts the ones the engine would call that
  name; it never asks whether the other spelling's path exists. On macOS and
  Windows that path resolves to the user's own file, so an `Lstat` of
  `zz_bound.pk3` beside `ZZ_Bound.pk3` reports a second archive that is not
  there — which failed both native jobs on `2efa71f` with nothing wrong in the
  product (`NEW_323C`). `TestAFileOfTheBoundNameInTheGameFolder` is the pattern.
- **`GameFolder` copies none.** It is one file. Bound packages are recorded
  `not_staged` with where they would have to be, and a run repeats it.
- **Staged is not granted.** Local engine use is not redistribution. `q3pack`
  plans from the build's staged data and its grants and never from an
  installation; `TestABoundPackageStagedForTheEngineStaysOutOfTheMapsArchive`
  pins that.

The record is `aucom.q3-install/1.1`; `1.0` is still read.

## Whether it ran

**A live process is not a loaded map.** `q3run.Launch` reads the engine's output
as it arrives against the profile's own rules: a rule marked
`"signal": "map_loaded"` is acceptance, an error rule first is a refusal (and
the idle engine is stopped), and silence is `not_observed` — never success. The
line that means "loaded" is each engine's own, so it lives in the profile
document; do not hard-code one in Go, and do not report `accepted` for a profile
that declares none.

**ioquake3 demands `pak0.pk3` whenever the base directory is called `baseq3`,
and `com_standalone 1` does not change that** (measured, six variants). A free
standalone game uses another base directory name, sent as `+set com_basegame`
through the action's `base_game` option, which `q3run` fills from the
installation. An earlier draft of this work shipped a `standalone` option on the
strength of the cvar's name; it was wrong, and it was found by running it.

**The engine's game root is the installation's base path, per job**
(`job.Request.Roots`). `fs_homepath` is still not sent
(`aucom.quake3-maturity`): a player's settings stay where the engine puts them.
`q3run.Preflight` is `engine.Checker` without the game-folder faults, because a
check that knew only the name `baseq3` would refuse a game the engine runs.

**Build & Run refuses a Quake III pipeline** and names this path
(`errPlayIsNotForQuake3`). It stages a loose level into a mod folder, which is
not how a Quake III map is played; do not teach it to.

## What is not established

No Quake III client session with real game data has been observed: this
machine has none. `CL_InitCGame:` as the client's `map_loaded` line is the
published source's wording, not a measurement, and the profile says so. Windows
and macOS are compiled and unit-tested only.

**The page's Port field (moved from README.md, 2026-10-17).** On the Build page, *Package and run
this map* step 3's **Port** belongs only to actions that declare a port (the two servers): a client
action disables it, and a port typed for a server is not sent with a client run.
`GET /api/v1/q3/engines` names each action's declared options (`"options": ["base_game", "port", …]`),
which is what the page reads.
