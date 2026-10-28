---
title: "NEW_365: executor-owned CPU/RAM sampler, one-press Build & Run, latest playable build, atomic admission"
author: Andrea-dintino
date: 2026-10-28
branch: main
cost: { minutes: 77, tokens: 788011 }
touches:
  - internal/job/resources.go
  - internal/job/progress.go
  - internal/build/latest.go
---

## Decided
- CPU/RAM: one sampler per supervised TOOL job (`internal/job/resources*.go`), started in `Service.execute` after onStarted, stopped when reaped; viewers read `Service.Live` (GET /api/v1/jobs/{id} `live`, play-run `current_job_live`). Linux = the job's process group via /proc children walk + pgrp + start ticks; Windows = root process only (no Job Object exists); other = unavailable. Job record keeps only a `resources` summary (aucom.job/1.6).
- Progress is only what the tool printed: EricW `Calculating Base/Full Vis:` and `---- X ----` phases, `[NN%]` brackets; a phase change clears the percent.
- Build & Run (HITL 2026-10-28): label always "Build & Run"/"Build"; check runs by itself (play/plan, build/preview), planKey includes pipeline readiness + engine action problems/roots; "Checking setup…" beside the button; dialog only for public listing / Q2 own-files basis. Texture bundle download is NOT part of the check ("Check the textures now").
- Duplicates are refused server-side: `playrun.Live.claim` (409 already_running + run_id), web `buildRuns.claim`; hand vs auto-build via `autobuild.AdmitManual` (one state-file Update; failed start releases).
- `build.Latest`/`build.Diagnostic`: any build of a leak pipeline (binding, built-in adapter, pinned) is a diagnostic, never playable.

## Tried and ruled out
- GNU `timeout` in a bounded CPU helper: it moves its child into a NEW process group, so the sampler (correctly) does not count it; burn CPU in the wrapper's own shell.
- Re-rendering the status sentence on every poll: it replaced the Set up link under the pointer; redraw only when its text/href changes.

## Watch out
- A text field's `change` on blur fires when the user clicks Build: keep the check when the request is unchanged (build.js `checkedRequest`, play.js invalidate early return), or the button disables under the click.
- Editing a bound tool's bytes flips readiness until the Companion re-probes it.
- AUB revision addressing in `aub:map/<id>@<x>` takes the revision ID, not the number; AUB exports textures only for the current revision.
