---
title: "A leak request opens ready to Build, and only the fork documents ship; programs never migrate"
author: Andrea-dintino
date: 2026-11-02
branch: main
id: l-b5e6f5e6
scope: repo
status: active
src: agent
touches:
  - internal/web/assets/leakrequest.js
  - internal/web/leaktest.go
---

## Decided
HITL NEW_380: no Review step for a leak request, and the built-in compilers are the Auto-Pigeon builds only. The page prepares a request itself through the existing form; `reviewing` stays the wire value and means the form's check passed. Bindings carry folders and arguments once; programs never, because a name or banner does not prove a fork. An unpinned game keeps the chooser (NEW_310's "no automatic pin"), marked for HITL review.

## Tried and ruled out
Checking the source form to stop "Configure a game" in front of Build: a signed-out page also opens on the file form and is meant to be asked. The question waits for the first read of the leak request instead.
Chrome's external-protocol hop failing on Xvfb is not xdg-utils: Chrome inherited the operator's D-Bus session. Start the driver under `env -i` with the task HOME and XDG paths.

## Watch out
A preview must refuse what the run refuses, or Build is offered and the press spends the editor's request: `q3vfs.CheckLayout`.
The installed release can move past the release a fork document is named for, and the Quake II reader qualifies exact banners, so a sealed Quake II map can read "No verdict". That is the honest reading of an unmeasured release, not a defect of the page.

In the code: `internal/web/assets/leakrequest.js`, `build.js` (`leakBound`, `dropLeakRequest`), `internal/web/leaktest.go` (`leakRevision`), `internal/retiredprofile`, `internal/leakintent` (retired ids), `testdata/leakjourney.js`.
