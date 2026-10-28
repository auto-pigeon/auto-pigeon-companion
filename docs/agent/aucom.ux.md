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
  - internal/web/assets/profiles.js
  - internal/web/assets/play.js
  - internal/web/assets/autobuild.js
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

## Build & Run is one form with one primary (NEW_363)

- **One journey, one form.** Map (saved map | file on this computer) → Build profile → Run after build → Review.
  No wizard of steps and no second build page: `#build` is the form on a file, `#autobuild` its checkbox
  (`app.js` `aliases`). Run, Live Games, My Maps, Profiles and Jobs are destinations, not builds.
- **The primary says what it does, always** (HITL 2026-10-28, NEW_365 — supersedes NEW_363's two-press
  *Review build*). It reads **Build & Run** when Run after build is on, else **Build**, and never changes label.
  It is enabled once the server's check of these exact choices passed (`play.js` `refreshPlan`/`runCheck`,
  `build.js` `preview`/`scheduleCheck`): the check runs by itself 250 ms after a change, one at a time, and an
  answer for choices or a setup (pipeline readiness, engine action problems and roots, grants) that changed
  since is dropped (`planKey`, `setupKey`). Meanwhile the sentence beside it says **Checking setup…**; when
  blocked it says the one reason with its **Set up** link. Never a tooltip. The check starts, launches and
  downloads nothing — the texture bundle is the run's first step, or **Check the textures now** in Technical
  details.
- **One press, one run.** The press says **Starting…** in the same instant and the run's card follows. A second
  press, another tab or a retry of the same request is refused by the server (`playrun.Live.claim`,
  `buildRuns.claim`: `409 already_running` with the run) and the page follows the running one; a hand build of
  the revision Auto-build is building with the same profile is refused atomically (`autobuild.AdmitManual`).
- **A decision only the person can make is a focused dialog, and only then:** listing a game for everyone, or
  whose own files a Quake II package may carry. Its action at bottom right says the same **Build & Run** /
  **Build**, with **Cancel** and the × (core.js `confirmModal({content, closeX})`). No recap dialog otherwise.
- **Details fold.** What only the person can answer (a WAD not sent, a Quake II package's files, a listing) is in
  the open; downloads, programs and versions, writes and argv are under **Technical details**.
- **A run gets one card** with its pinned identities from the run record (never the form's draft) and one
  primary: **Try again** when it failed and can be retried, else **View progress**.
- **Automatic builds are a checkbox under the build profile,** using that same profile and the same readiness
  (`pipelineReady`); a different profile applies only through **Apply to automatic builds**.
- **A server failure is said by its code** (`internal/web/failurewords.go`: status + AUB reason, never the
  English), each with its own action; the server's words fold under *What the server answered*.
- Budget: the ordinary viewport carries at most 90 authored explanatory words (measured 38 at 1440x900 with
  the compiler's name included; the five-step page carried 91 on step 1 alone).

## Profiles: one row, one state, one page in task order (NEW_364)

- **A row is a name, a kind and game, and ONE state** — the server's `setup_state`, said as *Ready*, *Partly ready*
  (*Ready: Play a map only* for one startable action), *Needs setup*, *Needs approval*, *Incompatible program*,
  *Unavailable on this platform*. The name is the row's link; no Configure button per row. A pipeline row names the
  tool it waits for.
- **A page answers what it is for, what to configure, whether it can be used** — Setup, Options, Approval — and keeps
  trust, provenance, exact versions, paths, digests, commands and Remove under a closed **Details**.
- **Nothing executes from a list:** selecting a row or opening a page starts no program (job count unchanged, live).
- Measured at 1440x900 (authored words in the area, names included): AP 1.4535 tool with an incompatible light
  **626 → 205**, Q1 normal pipeline 267 → 98, vkQuake 451 → 150; the list's 6 Configure + 6 Homepage buttons per tab
  → 0 (row links).

## Jobs, Activity and a running tool (NEW_365)

- **A job row is its name (the link), one state word, when and how long, and at most one control:** Stop while it
  runs. No Open button beside the link; a failed row adds its kind of failure.
- **A job's page** is state, times, the live line, the error, findings, outputs, Stop / Run this again, then the
  output; command, program, folders, digests, exit status and pid are under a closed **Technical details**.
- **The live line is one rendering** (`core.js` `liveLine`) of one executor-owned sampler, shown by Jobs and by the
  Activity card: `VIS · Full VIS 37% · CPU 285% · RAM 640 MiB · last output 4 s ago`. A percentage only when the
  tool printed one, per phase; quiet is said as *running; no output for N*, never *hung* or *healthy*.
- **Activity's run name is the link to the running stage's job;** the card's live line is redrawn as text, so a
  focused Cancel survives the poll.
- **Play latest build** appears on a My Maps card only for a verified normal build (`build.Latest`), says the
  revision it was built from and, when older, *Built from revision N; newer changes are not included.*; a map
  with only leak tests says *No playable build yet: only leak tests.* once. It opens Run on that build; nothing
  starts until Start.

## Examples

| | before | after |
| --- | --- | --- |
| selected vs unrelated | four paragraphs of other pipelines' reasons under the select | `ready` + the selected name + its summary; "Other pipelines (4 need setup)" closed |
| summary vs diagnostics | "Compile the map: \"Compile the map\" needs …" | "ericw-tools 2.0.0-alpha11 (Quake 1): qbsp, vis, and light not set up yet. Set up" + Details |
| review vs execute | **Pin and review** / **Not now** | **Use pipeline** / **Cancel**; the review in Build still ends at **Build** |

## Tests that hold it

`testdata/playjourney.js` (one form, F1/F2 refusals, late-answer fence), `new363_test.go`,
`testdata/profilespage.check.mjs` and `new364_test.go` (Profiles state, fences, argument revisions),
`testdata/setupcauses.check.mjs` (`TestWhatTheUnreadyChoicesNeedIsSaidOnce`),
`testdata/leakrequest.check.mjs` cases 12–14, `TestAnUnreadyChoiceNamesTheToolToSetUp`.
NEW_365: `testdata/playjourney.js` (Checking setup…, a held plan answer never enables the button, reason + link
beside it, two fast presses → one run), `testdata/hostjourney.js` (the listing dialog: Cancel starts nothing),
`testdata/liveline.check.mjs`, `testdata/buildreview.check.mjs`, `internal/playrun/new365_test.go`,
`internal/autobuild/new365_test.go`, `TestAFileBuildRequestIsAdmittedOnceUntilItEnds`.
