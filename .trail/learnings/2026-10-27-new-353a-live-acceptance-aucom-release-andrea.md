---
title: "NEW_353A live acceptance: AUCOM release flows, leak link, linked-session Save, isolated --prod preflight"
author: Andrea-dintino
date: 2026-10-27
branch: main
cost: { minutes: 130, tokens: 1090944 }
touches:
  - ".github/workflows/build.yml"
  - internal/publish/publish_test.go
  - internal/publish/install.go
---

## Decided
- AUCOM main may be pushed whenever a task needs its CI/release (HITL 2026-10-26); rule lives in docs/agent/aut.branch-policy.md and run-agent.sh's branch sentence.
- Live Companion acceptance uses the RELEASED zip (gh release download + sha256sum -c), never a local candidate: the page footer shows the version and HITL checks it.
- Headed browser work: task-owned GPU Chrome through scripts/aup/lib/cdp.mjs (copy of NEW_348 live/driver.mjs); the operator's own Chrome has WebGL off.
- Install identity: the recorded binding origin decides (AUCOM b04e6d1); trust is overwritten to `local` by approve/save-paths writers.
- `.trail` is skipped by env_contract's fallback scan like graft (AUT a3f494c).

## Tried and ruled out
- Typing into AUP/Companion fields with click+type: modal scroll offsets eat it; use the HTMLInputElement value setter + input/change events, then read the value back.
- Expecting `--prod` to honour AUB_DATA_ROOT: it does not; without an explicit AUB_DATA_DIR it resolves the operator's runtime/auto-pigeon-backend/aup127-prod (check --print-config first).

## Watch out
- AUP Test in Companion: Chrome's external-protocol prompt is browser UI; cancel with an X click (Xlib xtest), read the request id from performance resource timings (`/api/companion/v1/leak-status/<id>`) and run `companion game open <autopigeon://leaktest/...>`.
- Companion Build & Run's mod folder defaults to `auto-pigeon` and REPLACES the Companion's previous install there; always set and verify play-mod before pressing Build.
- A bound invitation on a no-mail deployment needs the operator to mark the account verified, whatever the signup page says.
- An isolated --prod launch's preflight reads the repositories' .env, not AUP_ENV_ROOT copies; offset 700 worker status ports (9990/9991) collide with offset 800's AUB.
- Bring a CDP tab to front (Page.bringToFront) before screenshots; a background tab never answers captureScreenshot.
