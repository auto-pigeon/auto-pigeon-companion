---
title: "NEW_368 post-push: lockfile under OS guard; handoff acks correlated by per-press delivery ids"
author: Andrea-dintino
date: 2026-10-29
branch: main
---

## Decided
- internal/lockfile keeps the O_EXCL record (holder diagnostics; released Companions read it) but every change of it — create, judge+break, heartbeat, release — runs holding the OS lock (flock / LockFileEx) on a never-removed `<lock>.guard`. Primitive extracted from tooluse into internal/oslock (no import cycle). `With` returns ErrLost when the body succeeded but its lock was lost.
- Editor handoff acknowledgements are tied to a press of Open by a per-press DELIVERY ID minted by AUB (`POST /api/companion/v1/handoff-attempts/{request}`) and written in place of the request id in the unchanged link shape. AUB resolves it for build read/status and leak status/result; acks stay under the posted id. Works with released Companions (v1.332 acks land per press) with no AUCOM protocol change.
- Duplicate-build guard: no attempt once a build started; a delivery id read after a build started under another id answers `blocked` / `another_link_is_building`.
## Tried and ruled out
- Per-request ack sequence + "count only newer than last seen", even with a fresh GET before reopening: a POST delayed past the GET is still numbered as newer.
- An `attempt=` query field or `#fragment` in the link: buildlink.go/leaklink.go refuse unknown keys and `#`, so every released Companion would reject Open again.
## Watch out
- Install lockfile.Hooks before the goroutine that reads them starts (heartbeat test hit a -race report otherwise).
- Companions ≤ v1.332 do not take the guard: exclusion is only among guard-taking Companions; manual upgrade must close the old one first.
- The self-update concurrent-start regression waits out the loser's 5 s lock timeout by design.
