---
title: "A build's request is released with its terminal record, and a claim is owned by a token rather than its key"
author: Andrea-dintino
date: 2026-10-31
branch: main
cost: { minutes: 48, tokens: 722218 }
id: l-d1147fab
scope: repo
status: active
src: agent
touches:
  - internal/web/builds.go
  - TEST-SKILL.md
  - internal/playrun/service.go
  - internal/playrun/execute.go
---

## Decided
- Build & Run: the terminal record and the release of the request key are ONE step under the registry lock. The worker entry stays until its goroutine exits, marked ended, so shutdown still waits for it while it excludes nobody and is not listed as running. A request key and a worker's lifetime are two different things.
- Cancel decides again under that same lock and re-reads the record. Its first read is only a reason to ask.
- The retry step ("is there one yet?" plus starting it) belongs to the shared registry, not to a coordinator: the web layer builds a coordinator per request, so a per-coordinator mutex serialised nothing.
- File builds use a different shape on purpose: a refusal first asks what a read would ask, and an ended holder is replaced. The runner writes the terminal manifest before it returns, so tying the release to `finish` could never close the earlier window, and wrapping that write would need a new hook in the shared build package and disk I/O under the registry lock.
- A claim is owned by its pointer, not its key: a build has no id when it claims, and an old goroutine's late release or rename must not touch the build that replaced it.

## Tried and ruled out
- Reversing the two lines (release, then save): a Cancel then finds an active record with no worker and writes Cancelled over a success. A negative control reproduces it.
- A lock around save + release alone: Cancel can still act on a snapshot it read before that section.

## Watch out
- "Active with no worker" is what an abandoned run looks like, and it is also what a failed terminal write leaves. Both answer the same way: Cancel records cancelled, Recover records failed.
- The terminal write can hold the registry lock up to `fsshare.Patience` (2 s) on Windows if the record is busy. Nothing else in the section can block.
- A file-build refusal during the moment before the holder has an id names nothing (`build: ""`). That is the old contract, and there is no id to give.
- A test that holds a window must prove it held it. The crowd test first armed its hold on the wrong build and passed on the original code.

In the code: `Live.publish`, `Live.stopOr`, `Service.conclude`, `Service.abandon` in internal/playrun; `claimBuild`, `buildHasEnded`, `buildClaim` in internal/web/builds.go. Private seams: `Store.beforeSave`, `Service.cancelRead`, `buildRuns.passed`.
