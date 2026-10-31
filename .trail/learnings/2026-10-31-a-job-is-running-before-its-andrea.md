---
title: "A job is Running before its goroutines exist, so a baseline taken at Running can be four short"
author: Andrea-dintino
date: 2026-10-31
branch: main
cost: { minutes: 26, tokens: 187576 }
id: l-8d9a19f7
scope: repo
status: active
src: agent
touches:
  - internal/job/resources_linux_test.go
  - internal/job/exec.go
  - internal/job/service.go
  - TEST-SKILL.md
  - internal/job/live.go
  - internal/job/resources.go
---

## Decided
TestManyViewersShareOneSamplerAndAnEngineHasNone counts what a viewer caused, not how many goroutines the process has. Viewers run under a runtime/pprof label, which anything they start inherits, and a settled process-wide count stays as a backstop with no allowance. No production change: Running being visible a moment before the executor's goroutines is harmless.

## Tried and ruled out
"Viewers were still exiting after WaitGroup.Done" was NEW_371A's recorded cause and the first explanation NEW_371C inherited. It is wrong for the CI failure: that race gives 12 to 15. CI's 8 to 12 is reproduced exactly by delaying the executor after Running, with zero viewers alive and zero job goroutines in the baseline. A settle poll alone would never have cured it, because 12 does not come back down to 9.

## Watch out
A normal baseline here is 12. Running is recorded inside onStarted; the sampler, both output readers and the waiter start after it, in that order, so the Cmd.Wait goroutine is the proof all four exist.
A negative control that fires once on the first Live call fires on the engine's read, not a viewer's, and leaks into the baseline: scope a mutation to the viewers' job or it proves nothing.
The same window can make a Live straight after Running return no Resources, because the sampler is put in the map after step(Running). Reported in NEW_371C, not fixed.
Residual: a plain goroutine started from an unlabelled existing goroutine and exactly offset by an unrelated harness exiting is not caught. The test has no such exit, so this took an injected one.

In the code: internal/job/resources_linux_test.go, internal/job/exec.go execution.run, internal/job/service.go onStarted. Evidence: mapper/LLM/manual_tests/NEW_371C/negative-controls/.
