---
title: "NEW_367 managed EricW/q3map2 install and updates: one closed owner, gated activation, live fixture transport"
author: Andrea-dintino
date: 2026-10-28
branch: main
cost: { minutes: 73, tokens: 1271554 }
touches:
  - internal/managedtools/tools.go
  - internal/managedtools/github.go
  - internal/managedtools/eligibility.go
  - internal/managedtools/extract.go
  - internal/managedtools/state.go
  - internal/managedtools/install.go
  - internal/managedtools/arch.go
  - internal/managedtools/manager.go
  - internal/managedtools/status.go
  - internal/managedtools/managedtoolstest/fixture.go
---

## Decided
- `internal/managedtools` is the ONLY downloader (HITL 2026-10-28, ADR-0010): compiled allowlist of auto-pigeon/ericw-tools + auto-pigeon/q3map2, GitHub hosts only over HTTPS; `NewLoopbackSource` refuses non-loopback; the env seam `AUCOM_FIXTURE_RELEASES_URL` exists only in `-tags aucom_fixture_releases` builds.
- Verification order: release-manifest (schema/tag/version/target/acceptance) vs GitHub listing + GitHub asset digest → archive sha → bounded zip into `.staging` → every file vs archive `MANIFEST.json` → ELF/PE/Mach-O arch → one rename → `job.Service.ProbeAt` (executor's probe) → only then `web.bindManaged`.
- Binds ALL built-in profiles of the tool nobody chose (upstream-named defaults too, so built-in pipelines use it) in one `binding.Update`; binding mode `managed_official` + `managed` (local-binding/1.6). A person's program clears `managed`.
- Activation waits on `Host.Busy` (jobs pinned under the old dir + `Server.toolGate`, held by every `build.Runner.Run`) and writes under the gate's exclusive side. One coordinator per config.
- Bare `companion`/`--stay-running` pass `--managed-tools on`; `serve` defaults off.

## Tried and ruled out
- Watching the setup card against real GitHub: both archives install in ~3.5 s, before a page loads. Use the fixture server's `/control/hold`.
- Caching an evaluation behind the ETag when a manifest FETCH failed: a 304 replayed the transient failure. Clear the ETag then.

## Watch out
- `internal/cli` tests start `serve --interactive`: TestMain sets `AUCOM_MANAGED_TOOLS=off` and its own XDG_DATA_HOME — keep it, or tests hit GitHub and the real data dir.
- In live scripts `$!` of `(timeout … companion &)` is the timeout wrapper: `kill -9` on it leaves the Companion running. Kill `pgrep -P <wrapper>`.
- Live fixture server: `AUCOM_FIXTURE_RELEASES_DIR=<dir> go test -run TestServeFixtureReleasesForALiveTask ./internal/managedtools/managedtoolstest/` (url.txt, control.txt, stop.txt).
- Retry-After must be resolved by the coordinator's clock (`RateLimitError.Until(now)`), not time.Now in the source.
