---
title: "NEW_368 Companion self-update: third target on the managedtools coordinator, journaled in-folder swap, admission fence"
author: Andrea-dintino
date: 2026-10-28
branch: main
cost: { minutes: 62, tokens: 1498634 }
touches:
  - internal/managedtools/self.go
  - internal/selfupdate/selfupdate.go
  - internal/web/selfupdate.go
  - internal/cli/selfupdate_cmd.go
  - internal/managedtools/managedtoolstest/companion.go
  - internal/managedtools/self_test.go
  - internal/selfupdate/selfupdate_test.go
  - internal/web/new368_test.go
---

## Decided
- The Companion's own release is `managedtools.Self` (Kind application) on the SAME coordinator/source/state as EricW/q3map2; never a first install of itself; separate offer/skip key `companion`. One consolidated dialog for all offers.
- Only `internal/selfupdate` writes the Companion's folder: root from `InspectSelf` (executable), members = old+new bundle-manifest only, journal in `<bundle>/.companion-update/`, prove in place (`companion selfupdate prove`), rollback on failure / interrupted swap / 3 unready starts.
- No Windows helper: the ending process renames its own running .exe aside (allowed on Windows) and places the new one; POSIX hard-links aside so the path never disappears.
- `serve` returns an after-exit func: relaunch must happen AFTER every defer, or the old process deletes the new one's api-token/api-url.
- Update now: wait for ActiveWork + toolGate + any toolchain install, then close the admission fence (503 companion_updating, autobuild too), recheck, stop with ExitUpdate.

## Tried and ruled out
- Restarting while a toolchain download was in progress (found live in headed Chrome): the new process resumed it, but the prompt forbids cutting installs off — `Manager.toolWork()` now blocks the restart.

## Watch out
- Live fixture: `managedtoolstest` `/control/publish-companion?version=&companion=&extractor=`; build fixture binaries with `-tags aucom_fixture_releases -ldflags -X main.version=1.90N`; `AUCOM_FIXTURE_FAIL_SERVE=<v>` makes that version refuse to serve (rollback test). `/control/hold` holds EVERY .zip, the Companion's too.
- A manual `tools check` within a minute of the last check is refused ("checked less than a minute ago"); a restart does not trigger a check.
- The fast-preview pipeline input is `source_map`, not `map` (a wrong name fails instantly and looks like "idle").
- Closing one of two MCP tabs can dissolve the Chrome tab group; the last tab then cannot be closed by the tool.
