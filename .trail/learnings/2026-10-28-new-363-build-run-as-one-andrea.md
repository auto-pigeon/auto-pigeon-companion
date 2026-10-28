---
title: "NEW_363: Build & Run as one form; server failures by code; live AUCOM journeys on an isolated copy of a task stack"
author: Andrea-dintino
date: 2026-10-28
branch: main
cost: { minutes: 84, tokens: 1419182 }
touches:
  - internal/aub/texture.go
  - internal/autobuild/new363_test.go
  - internal/autobuild/service.go
  - internal/autobuild/service_test.go
  - internal/autobuild/state.go
  - internal/playrun/execute.go
  - internal/playrun/record.go
  - internal/playrun/service.go
  - internal/texturebundle/damaged_test.go
  - internal/texturebundle/extract.go
---

## Decided
- Build & Run is ONE form (Map | Build profile | Run after build | Review) with one primary: "Review build" until `reviewed()` (plan key == form key), then "Build & Run"/"Build"; a start drops the plan. `#build` is an alias for `#play/local` (app.js `aliases`); the file source is still build.js, mounted in the same blocks.
- Server failures are classified once in internal/web/failurewords.go from HTTP status + AUB `data.reason.code` (never English); the page maps `code`/`failure_code` to t() sentences. A legal 403 stays 403 (aubStatus), not 401.
- Auto-build uses the form's profile + `pipelineReady`; server refuses unready on enable/pipeline and `Deps.Ready` holds dispatch; hand builds are noted (`NoteManual`) so the poller never rebuilds them.

## Tried and ruled out
- Driving the live journeys in the operator's Chrome via the extension: JS works but screenshots time out on the hidden window; use a task headed Chrome on its own Xvfb via AUT lib/cdp.mjs.
- 503-then-retry on a NON-current revision: AUB exports textures only for the current revision, so the retry is (correctly) `revision_changed`. Use the current revision with its bundle removed from the task cache.

## Watch out
- CDP Page.navigate to the identical URL does not reload: add a fresh query (`?u5=3`) or the page keeps an old API token.
- Copying a task stack's pb_data to `pb_data-<9190+offset>` under a new AUB_DATA_ROOT reuses its accounts/maps/WADs (NEW_357 → NEW_363 at offset 363).
- `pgrep -f vkQuake` matches your own shell line; check with `ps -p`.
