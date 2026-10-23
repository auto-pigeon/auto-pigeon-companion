---
id: aucom.ux
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: One task per surface, the selected choice in the open, everything else one click away
authority:
  - aucom-ux-copy-and-disclosure
topics:
  - ux
  - copy
  - wording
  - disclosure
  - dialog
  - readiness
  - needs-setup
  - setup-link
  - leak-chooser
  - i18n
paths:
  - internal/web/assets/core.js
  - internal/web/assets/leakrequest.js
  - internal/web/assets/index.html
  - internal/web/assets/app.css
  - internal/web/assets/locales/it.js
---

# One task per surface, the selected choice in the open, everything else one click away

`NEW_345` came from one screenshot: the leak-test chooser with one READY pipeline selected, four
built-in pipelines that needed setup, a 321-word dialog that said the same missing compiler four
times, links to four pipeline pages where nothing could be set up, and its action footer below the
bottom of the window. Nothing was wrong in any single sentence. These rules are what fixed it, and
they bind every surface that offers a choice, says why something cannot start, or asks to run.

## The rules

- **Name the task and the action.** Before changing a surface, say its primary task in one sentence
  and its primary action in one verb phrase ("Choose a leak-test pipeline" / **Use pipeline**). The
  ordinary state is a title, at most two short sentences, the controls, the selected choice's state
  and the actions. Aim at 60 authored visible words for an ordinary dialog, not counting names and
  the mandated work-in-progress, legal and approval notices; never game the count.
- **The selected choice is said in the open; the others are not.** What the SELECTED item needs
  appears beside the control (`leak-pin-status`). What unrelated items need is ONE closed disclosure
  under the list — `core.js` `setupActions`, labelled by `SETUP_LABELS` ("Other pipelines (4 need
  setup)") — and an option's own label says only `— needs setup`. Never concatenate problem
  summaries into an option or a paragraph.
- **A setup link goes where the fix is.** A pipeline problem carries the tool profile, the stage and
  the program it is about (`readiness.go` `problemOrigin`: `profile`, `profile_name`, `step`,
  `executable`). `setupCauses` groups by those ids and by `fault`, and links `#profiles/<tool>`. An
  engine's action is set up on the engine (`setupProfile`). Never derive a group, a program name or
  a destination from the English of a summary.
- **Summary first, exact words on demand.** A row's cause is one line in the page's words; the
  server's exact sentences and fixes stay under **Details**, unedited. Compiler output, argv, paths,
  digests and versions are never translated, shortened or hidden when they decide what runs.
- **One fact, one statement.** If a line above already says why, a second badge, banner or sentence
  below it is removed, not reworded (`profiles.js` provenance, `play.js` not-sent WADs).
- **Dialog shape.** `.dialog--split`: head (title, helper, `×` top right), one scrolling body, a
  footer that stays in view with **Cancel** bottom left and the primary bottom right. Focus is
  trapped while open and returned on close; Escape closes; closing changes nothing.
- **Dead ends are not offered.** With nothing ready, the list is replaced by one sentence and the
  open setup rows; the primary stays disabled. A list that cannot be read is one message and one
  **Retry**, never a blank select.
- **Review is not execute.** Choosing or pinning runs nothing, and its copy never says it will
  ("Next, you review the test; nothing runs until you press Build"). Execution keeps its own
  explicit confirmation; no surface replaces it with a vague Continue.
- **An answer belongs to the opening that asked.** A late response — a list, a pin — for a dialog
  that has since closed or reopened for another request neither closes the new one nor starts a
  review (`pinSession`).
- **Every sentence through `t()`, in both languages.** A count gets a singular and a plural key; an
  Italian phrase avoids agreement it cannot know ("qbsp, vis e light ancora da configurare").
  Leak test is *test delle perdite*, as in AUP.

## Examples

| | before | after |
| --- | --- | --- |
| selected vs unrelated | four paragraphs of other pipelines' reasons under the select | `ready` + the selected name + its summary; "Other pipelines (4 need setup)" closed |
| summary vs diagnostics | "Compile the map: \"Compile the map\" needs …" | "ericw-tools 2.0.0-alpha11 (Quake 1): qbsp, vis, and light not set up yet. Set up" + Details |
| review vs execute | **Pin and review** / **Not now** | **Use pipeline** / **Cancel**; the review in Build still ends at **Build** |

## Tests that hold it

`testdata/setupcauses.check.mjs` (`TestWhatTheUnreadyChoicesNeedIsSaidOnce`),
`testdata/leakrequest.check.mjs` cases 12–14, `TestAnUnreadyChoiceNamesTheToolToSetUp`.
