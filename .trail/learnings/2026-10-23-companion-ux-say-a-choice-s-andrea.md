---
title: "Companion UX: say a choice's setup state once, by structured identity"
author: Andrea-dintino
date: 2026-10-23
branch: main
cost: { minutes: 101, tokens: 850575 }
touches:
  - README.md
  - internal/web/assets/app.js
  - internal/web/assets/build.js
  - internal/web/assets/core.js
  - internal/web/assets/leakrequest.js
  - internal/web/assets/locales/it.js
  - internal/web/assets/play.js
  - internal/web/assets/profiles.js
  - internal/web/assets/run.js
  - internal/web/leakpipeline_test.go
---

## Decided
- Lists of things that cannot be chosen (pipelines, engines, actions) use core.js `executionChoices` → `setupActions`: option label only "— needs setup", ONE closed "Other … (n need setup)" disclosure, rows from `setupCauses`, link via `setupLink` to the profile that must be fixed. The selected item's state is said beside the select by the caller, and is excluded from the "Other" count.
- Group/phrase setup causes only from structured fields the server sends on pipeline problems (`profile`, `profile_name`, `step`, `executable`, `detail` in internal/web/readiness.go). Never regex or cut the English `summary`.
- Dialogs that must keep their action visible use `.auth-dialog.dialog--split` (head / one scrolling body / footer, Cancel left, primary right) and core.js `trapFocus`. Rules live in docs/agent/aucom.ux.md.

## Tried and ruled out
- Radio-card list for the leak chooser (prototype B): second version of a select, grows with every ready pipeline.

## Watch out
- locales/it.js must stay JSON-shaped: no `//` comment lines inside the dictionary (TestEveryOfferedLanguageHasADictionaryThatKeepsItsSlots).
- Build & Run keeps its choices in the URL query; navigating a test browser to a bare `#play` resets them (not a product bug).
- A synthetic `autopigeon://leaktest/map/…` link needs a 32-hex `request=` id or "reviewing" is refused with 409.
- The CDP driver's `key` op sends no `text`, so Enter does not activate a <summary>/button; send a keyDown with text "\r".
