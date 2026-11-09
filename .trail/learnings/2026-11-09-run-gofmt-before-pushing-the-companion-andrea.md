---
title: "Run gofmt before pushing the Companion: a gofmt failure silently skips the threat-model and release-bundle CI jobs"
author: Andrea-dintino
date: 2026-11-09
branch: main
cost: { minutes: 17, tokens: 157117 }
id: l-b03c8f20
scope: repo
status: active
src: agent
touches:
  - internal/threat/rows.go
  - README.md
  - cmd/mapio/canonical.go
  - cmd/mapio/canonical_test.go
  - cmd/mapio/canonical_wad_test.go
  - cmd/mapio/canonical_windows_test.go
  - internal/cli/installed_ericw_aub_test.go
  - ".github/workflows/build.yml"
---

## Decided
- Before pushing AUCOM main, run `gofmt -l .` locally and fix what it lists. The build workflow's gofmt job gates the later jobs, so one misaligned struct literal turns them into "skipped", not "failed", and a run looks mostly green while the threat-model and release-bundle checks never ran.
- When re-verifying another session's uncommitted security work, run the negative-control mutations (boundary bypass, policy off, single-rule removals) in a scratch copy of the tree, never in the checkout. The prompt forbids reset/stash/overwrite of the dirty files, and a copy keeps the real tree byte-for-byte untouched.

## Tried and ruled out
- Treating a red build run as a regression from the newest commit: the NEW_434 run on 086dc30 had already failed only on gofmt (internal/threat/rows.go T59 alignment), while all three test matrix rows passed.

## Watch out
- The windows-latest `go test` row does not depend on gofmt, so native Windows test evidence still arrives on a run whose gofmt job failed; check jobs individually.
- After the NEW_440 WAD rule (every entry flattened to a file name), most ericw Q1 testmaps report SAME-WITH-REDUCED-WADS instead of SAME. That is expected, not drift.

In the code:
- `.github/workflows/build.yml` gofmt job; `internal/threat/rows.go` row T59
- scratch copy: `git ls-files -z --cached --others --exclude-standard | xargs -0 cp --parents -t <dir>`, then `go test -buildvcs=false`
