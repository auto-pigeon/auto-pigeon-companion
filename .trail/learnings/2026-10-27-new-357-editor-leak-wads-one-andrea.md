---
title: "NEW_357 editor leak WADs: one bundle owner, job-owned copy, real Linux handler hop on Xvfb"
author: Andrea-dintino
date: 2026-10-27
branch: main
cost: { minutes: 69, tokens: 774621 }
touches:
  - internal/web/play.go
  - internal/playrun/declaredwads.go
  - internal/web/leaktextures.go
  - internal/web/builds.go
  - internal/web/leaktextures_test.go
  - README.md
  - internal/build/tooltextures.go
---

## Decided
- An editor-requested Q1 leak build gets its textures from `Server.verifiedTextureBundle` (the ONE fetch/verify owner Build & Run uses), for exactly the pinned map id + revision, copied (re-hashed) into a job-owned `builds/leak-textures-*` dir passed as `content_root` (-wadpath); done BEFORE `leakintent.Consume`, so a refusal leaves the request pending. Never a page-typed folder, never a host WAD.
- `internal/build/tooltextures.go` owns the tool-texture list: ericw 2.0 prints `unable to find texture skip` on EVERY Q1 map; recorded as info so a sealed run is a complete result.
- Real handler proof on Linux: task HOME/XDG + Xvfb :77 (`runs/auto-pigeon/NEW_357/live/desktop/env.sh`), `companion uri register` in that env, headed task Chrome (AUT cdp.mjs) in the same env, Chrome's own prompt approved with an XTest click, handler argv caught from /proc.

## Tried and ruled out
- Deleting the WAD in AUP to make a bundle "unavailable": AUB refuses while a saved map references it; AUP's dialog re-skins faces to skip instead. Revoke the session server-side (superuser password rotation) after the review is open, on a revision whose bundle is not cached.
- Coordinate clicks on Companion page buttons: a "Sync needed" banner shifts layout between lookup and click. Use DOM `.click()` for in-page buttons; X-level clicks only for browser chrome.

## Watch out
- python-xlib/`import` without `-display :77` act on the OPERATOR's display (one stray click happened). Pin the display in the helper.
- A /proc watcher started from a heredoc matches its own shell (pattern in the command line); start it from a file.
- AUB's texture-export ZIP differs per export for one revision (different digest, same WAD bytes): an unchanged bundle digest across two runs proves cache reuse.
- `companion game open` starts google-chrome under the task HOME on every hop (backlog P3); cancel its first-run Terms, kill those processes at cleanup.
