---
title: "01_NEW_368 updater repairs: write-ahead self-update journal, cross-process tool leases, provenance-only Play latest"
author: Andrea-dintino
date: 2026-10-29
branch: main
cost: { minutes: 71, tokens: 1162986 }
touches:
  - internal/selfupdate/selfupdate.go
  - internal/selfupdate/crash_test.go
  - internal/cli/selfupdate_recover_test.go
  - internal/tooluse/tooluse.go
  - internal/tooluse/tooluse_test.go
---

## Decided
- Self-update journal /2 is written ONCE before anything moves (old/new SHA-256 per owned path); forward/reverse passes are idempotent and decide from the bytes at dest/previous/incoming. rolled_back only after reading every member back; otherwise recovery_needed (start refuses to serve, Apply refuses, nothing kept is removed). `companion selfupdate recover` derives the bundle from its own location so `.companion-update/previous/<companion>` can run it.
- A 1.323 (/1) journal is still read and MUST be confirmed in /1: 1.323's WaitVerified rejects any other schema.
- Cross-process protection lives in `<tools>/.in-use/` (internal/tooluse): every build.Runner.Run takes a use lease before touching programs, freezes Executables+digests per stage; activation/prune/self-update take a switch gate and keep it while waiting (writer preference) -> new builds get 503 tools_updating, Auto-build autobuild.ErrNotNow (revision not consumed).
- Play latest: build.Diagnostic = leak_test provenance or built-in leak pipeline. The pin is no longer an input.

## Tried and ruled out
- "Succeeded without VIS/LIGHT => diagnostic": made legitimate compile-only builds unplayable (TestFirstRunJourneyInABrowser). Reverted.
- lockfile's 60 s staleness after a killed updater: recovery at the next start timed out; added lockfile.Options.BreakDeadHolder.

## Watch out
- Web tests: every server must have its OWN tooluse registry (harness SetToolRegistry); a coordinator left waiting holds its gate (own pid = live) and blocks every later build in the binary. Call Manager.Close in cleanup.
- `pgrep -f "<serve args>"` also matches your own bash -c line; filter `grep -v bash` or anchor `^./companion`.
- Fixture-only seams: AUCOM_FIXTURE_KILL_AT=<point> kills the updater mid-swap; fixture qbsp honours `// AUCOM_HOLD_UNTIL <file>`.
