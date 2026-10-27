---
id: aucom.profile-authoring
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: Writing a profile — the New profile form, stage tools and stage arguments
authority:
  - aucom-profile-authoring
topics:
  - profile-authoring
  - new-profile
  - scratch
  - compose
  - stage
  - stage-tool
  - stage-arguments
  - pipeline
  - readiness
paths:
  - internal/profile/template.go
  - internal/web/scratch.go
  - internal/web/scratchfill.go
  - internal/web/composesetup.go
  - internal/web/profiles.go
  - internal/web/arguments.go
  - internal/web/assets/scratch.js
  - internal/web/assets/stageswitch.js
  - internal/web/testdata/stageswitch.check.mjs
prerequisites:
---

# Writing a profile — the New profile form, stage tools and stage arguments

These rules were user-manual paragraphs in `README.md` until HITL made it user-facing only
(2026-10-17). The README keeps a short user version; this is the full behaviour, and the code and
its tests (`internal/web/stagetool_test.go`, `internal/web/testdata/stageswitch.check.mjs`)
remain the authority when the two disagree.

## A stage names its tool, and is never run by another

- A pipeline stage names the tool profile that runs it (`"tool"` in the document), so a user's own
  EricW profile and the built-in one can both be installed: each pipeline runs the one its stages
  name, and the built-in pipelines name the built-in tools. **Only a stage that names no tool is
  refused when two installed tools provide what it needs.**
- A stage whose named tool cannot run it — not installed, not a tool, or without that step — is
  refused for that reason and is never run by another tool instead; nor when that tool's approval is
  withdrawn. The build record names the tool and digest that ran each stage.
- A stage whose tool is not installed at all installs (nothing can be said about its wiring) and is
  listed as needing that tool. A pipeline whose stages do not fit the installed tools (an input the
  tool does not declare, a required input left unwired, a parameter it does not have) is not valid:
  the form stays on the stages step and `POST /api/v1/profiles/compose` refuses it with HTTP 422,
  installing nothing, whoever sends it. A valid answer carries
  `setup: {"approved", "ready", "problems"}`.
- A pipeline starts nothing of its own, so it is ready when every stage's tool is approved and set
  up; its own approval is reported beside that, not folded into it. Starting a build with a pipeline
  whose setup is incomplete is refused with HTTP 409 (`pipeline_not_ready`) before anything is
  fetched or run, and no failed build is left behind.

## Changing a stage's tool compares; it does not reset

Choosing another tool keeps where each input comes from and every parameter the new tool also
declares, and says so under the choice (*Tool changed. Kept: …*). The stage's id, title, place in
the order and its own arguments are never the tool's and are never touched.

- An input the new tool declares under the same name keeps its source, provided that source
  produces the kind of file the input takes.
- An input or output the new tool declares under *another* name is carried over only when both
  tools declare exactly one port of that role — the role is the declaration. Position is never used,
  and two candidates are a question for the user, not a guess. A renamed output repoints the later
  stages and results that read it.
- A parameter keeps its value when the value passes the new tool's declaration (type, range,
  choices).
- Anything else opens a review inside the stage naming each field, its value and why the new tool
  cannot take it. **Nothing has changed** while it is open, and **Next** waits. *Keep the current
  tool* leaves the stage as it was; *Change the tool and revise this stage* removes what was listed.
  Nothing removed is remembered: choosing the old tool again does not bring it back.

A stage that is not complete says so where it is (*This stage is not complete: …*), and a field
holding a value nothing provides any more shows that value, marked, rather than an empty box.

## The form

- **Start from** is *From scratch* by default; a tested profile fills every field of every step, and
  what the form has no field for is kept as the tested profile had it. Profiles written on this
  computer are listed after the tested ones, marked *(installed here)* with their version, and bring
  their stages' own arguments. Keeping the name and ticking **Replace the installed profile with the
  same id** makes a new version; the same version can never be rewritten, and the new version needs
  its own approval. Another name is a separate profile.
- **Next** keeps the user at the current step when required fields or server validation fail.
  Installing opens the approval and setup page. Stage arguments travel with **compose**
  (`stage_arguments`) and are checked by the same refusal saving uses (`stageArgumentsRefusal`),
  so a token the stage already passes keeps the form at step 3, beside that stage's boxes, with
  nothing installed (`NEW_360`). If saving still fails after installing, the pipeline's own page
  opens with the unsaved tokens filled in and the refusal beside that stage — corrected there,
  never retried unchanged and never installed twice.
- Execution lists keep unavailable profiles and actions visible but disabled (`— needs setup`), with
  what each needs in one closed list under the select and a **Set up** link to the profile that is
  actually missing (`aucom.ux`).

## Stage arguments

Arguments are per stage of a pipeline (`POST /api/v1/profiles/<pipeline>/stage-arguments`,
`companion toolchain args <pipeline> <stage>`): another pipeline using the same tool does not get
them, and the same tool twice in one pipeline gets two lists. They are added before the stage's input
files, one argument per box, never split and never passed through a shell. The profile document is
not changed and an exported profile carries none of them. **A build tool takes no new arguments**:
ones recorded on a tool before stage arguments existed still reach every pipeline that runs it, are
shown on each such stage, and can be removed on the tool's page or with
`companion toolchain args <tool> <program> --reset`. An engine is not a stage and keeps its own. A
token repeating a switch the stage already passes is refused (`aucom.compiler-compatibility`).

## An engine plays the game its document declares (`NEW_330`)

- Whether an engine can play what a pipeline makes is `engineFits` (`internal/web/play.go`): the two documents'
  `game_profile.engine_family`, never a name, and a pipeline that declares no game is not judged. It is decided once,
  on the server: `GET /api/v1/build/pipelines` carries `engine_fit: {engine_id: {fits, reason}}` per row, and
  `playFits` refuses a plan or a run on the same word (HTTP 400), before anything is fetched. The page compares
  nothing itself — no family test and no name test in browser code (`Q2_020` finding 2: the page held only Quake II
  to it, so a Quake 1 build offered Yamagi as "Ready to start").
- An engine that does not fit is greyed out with its reason and gets **no** "Set up" link (`executionChoices`'
  `offerable`): setting it up could not make it fit. Changing the pipeline clears an engine that no longer fits, at
  once, and says why in `#play-engine-message`; the saved setup is untouched.
- The game's own folder in Build & Run copy comes from the engine's declared `content_layouts`
  (`EngineProfile.BaseFolder`: the first `loose_files` layout under `game_root`), never a literal `id1`.
- A launch is not a submitted job (`awaitEngineStart`): an engine job that ends unsuccessfully within
  `engineStartWindow` fails the run at `launching`, naming the job and its exit status (NEW_331 A1). One still
  running after the window is a started game.
- An engine's project folder (`content_root`), when a Q1/Q2 engine's paths are saved without one, is the Companion's
  own asset folder, created for it (`engine.FillProjectFolder`, page bind route and `companion engine bind`; HITL
  2026-10-22). A folder already recorded is never replaced; Quake III is excluded (its project folder is its package
  folder, `q3vfs.PackagesRole`). The value is absolute, so a copied config keeps pointing at the original folder.
- Profiles' readiness box says **Partly ready** and lists each blocked action whenever `action_problems` is non-empty,
  even though `readiness.ready` (the any-action rule) is true — "Ready: everything its programs need" is only for an
  installation every action of which can start.
- Build & Run keeps a chosen pipeline that loses readiness (`executionChoices(..., retain)` in `play.js`, as `build.js`
  does), and keeps the reason a family switch cleared an engine until an engine is chosen.
- A map-list 502 (AUB unreachable) is said in words with the address and **Try again** (`mapsUnreachable`).
- `tabsFor` redraws on `aucom:sync` and when the tab already holding the value is pressed: a deep link that sets a
  select's value (the `#new-profile/<id>` kind) must not leave another tab looking chosen.
