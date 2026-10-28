---
title: "NEW_364: Profiles as rows with one server-decided state; argument lists own their revision; pipeline exact commands"
author: Andrea-dintino
date: 2026-10-28
branch: main
cost: { minutes: 48, tokens: 601359 }
touches:
  - README.md
  - docs/agent/aucom.ux.md
  - internal/web/assets/app.css
  - internal/web/assets/index.html
  - internal/web/assets/locales/it.js
  - internal/web/assets/play.js
  - internal/web/testdata/profilespage.check.mjs
  - internal/web/readiness.go
  - internal/web/assets/profiles.js
---

## Decided
- A profile's state is decided ONCE on the server: `readiness.setupState` (internal/web/readiness.go) → `setup_state` (ready | partly_ready+ready_actions | needs_setup | needs_approval | incompatible | unavailable), fault order unavailable > approval > incompatible > needs_setup. The page words it via core.js `profileState`; never from a summary's English.
- A tool/engine's own program problems get `executable` from the document's action (`nameProgramsOf`, profiles.go) so setupCauses can say "light: not the version…"; approval faults (not_authorized/stale_binding) are said in words (core.js PROFILE_FAULTS) and only in the Approval section.
- Pipeline exact commands: `GET /api/v1/profiles/<pipeline>/commands` → `binding.StageCommand` (same planner as a tool preview; tool tokens then stage tokens = executor order).
- Argument editor: Save enabled only when values differ from the saved baseline; an answer applies to the values it was sent for; only the newest save's answer is drawn; Clear is local until Save.

## Tried and ruled out
- `-nopercent` as an example stage argument for Q1 qbsp: refused — the stage's verbosity parameter already passes it. Use `-nocolor`. `-bounce` on light is refused the same way (bounce parameter).
- Counting visible controls with `offsetParent`: content of a closed <details> still has an offsetParent in Chrome; use `el.checkVisibility()`.

## Watch out
- core.js `withBusy` restores the button's PRE-press disabled state in `finally`: recompute enablement after it (argument Save stayed enabled on a saved list).
- `bind` MERGES non-empty paths; an explicit "" clears one program.
- Signing in/out moves the page to #play; a late answer for Profiles must be fenced by `viewGeneration` (aucom:status), not by the area being visible.
- CDP key "Enter" without text does not toggle a <summary>; Space does. zenity on the task display: send Escape to the LARGEST zenity window (the 1x1 one is the leader).
