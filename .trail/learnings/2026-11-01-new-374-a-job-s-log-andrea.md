---
title: "NEW_374: a job's log vanishing at its end, and the Install then Configure order"
author: Andrea-dintino
date: 2026-11-01
branch: main
id: l-0217d1d9
scope: repo
status: active
src: agent
touches:
  - internal/job/output.go
  - internal/web/assets/tools.js
  - internal/web/assets/play.js
  - internal/selfupdate/selfupdate.go
  - internal/cli/firstrun_release_test.go
---

## Decided
TestARewrittenLogResetsTheReaderRatherThanSkippingIt on Windows CI 41236212023 was not a sharing violation and not the rewrite. Its error was ERROR_FILE_NOT_FOUND on workspace\run.log at the moment the job ended: a reader held a Running record, finish() saved the terminal record and removed the workspace, and sidecarPath had already looked. Every platform has this window. readSidecar now asks a fresh record once on fs.ErrNotExist and reads the artifact at the same offset.
Configure a game waits on tools.js installSettled, a promise of the whole Install interaction (window, download, notice), not on a dialog-open poll. Install Cancel or failure stays quiet for the rest of that visit: AUCOM.areaMoves tells a return from a same-area refresh.
selfupdate.Journal.Reason keeps why a rollback began through every later pass, because Detail on a resumed rollback used to be overwritten with "interrupted".

## Tried and ruled out
Waiting on the Install window closing alone: tellInstalled then stacks its notice over Configure a game. A refusal memory in play.js alone: a refresh that starts just after the Cancel finds no interaction in flight and asks at once, which is why ended outcome lives in tools.js with the move count.

## Watch out
After a press of Install the page used to keep its slow poll, up to 60 s, so install() must schedule(FAST_MS).
A negative control script that restores with git checkout must not be followed by a commit that assumes a patch applied: a failed assert left a control in a local commit once (amended before any push). Commit only after git diff shows the restored file.
A kill point in a rollback fault must leave the new program at its path, or serve goes on to serve forever and the test hangs: use restore:auto-pigeon-companion and go test -timeout.
The tools/check reset is not reproduced by Go client on Linux. The one transport failure found is a kept socket past IdleTimeout 120 s; whether that is the Windows report is unproved.
