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
  Installing opens the approval and setup page. If saving stage arguments fails, setup stays
  incomplete and **Retry saving stage arguments** saves them without reinstalling.
- Execution lists keep unavailable profiles and actions visible with their reasons but disabled,
  with a **Set up …** link per profile and **Profiles / setup** beside them.

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
