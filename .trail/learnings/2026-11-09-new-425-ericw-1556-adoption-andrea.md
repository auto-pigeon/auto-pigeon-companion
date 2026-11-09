---
title: "NEW_425 EricW 1.4556 pin: baseline moves the managed fixtures, the Q2 description is full, compare logs without progress ticks"
author: Andrea-dintino
date: 2026-11-09
branch: main
touches:
  - internal/managedtools/tools.go
  - internal/profile/builtin/ericw-tools-auto-pigeon-q2.tool.json
  - internal/leakadapter/testdata/q2/README.md
---

## Decided
- `Tool.Baseline` follows the fork profile's pin (aucom.managed-tools rule 3). Raising it makes every managed-tools fixture
  release below it `older_than_baseline`: the EricW fixture versions in internal/managedtools, web/new367, web/crossprocess and
  cli/firstrun_release tests were shifted +30 (1.4536 -> 1.4566 ...; the "older than the baseline" one is baseline - 1).
- The stale "second profile beside the built-in one ... duplicate a build profile" paragraph (false since NEW_380/ADR-0012) was
  dropped from both fork descriptions to make room for the 1.4556 sentence.
## Watch out
- The Q2 fork profile description is now exactly 4000 bytes (the limit, measured in bytes); platform notes are 200 bytes max.
- Comparing qbsp logs across releases: strip the banner, `time elapsed`, `N.NNNs seconds elapsed` AND the progress-bar ticks
  (`[ NN%]  est: ...`, `[.   ]`); the ticks depend on thread scheduling. Q1 runs also interleave hull progress.
- Q1 has no control set in AUCOM; the NEW_335 controls (LLM/runs/auto-pigeon/NEW_335/leak-controls) are the ones to re-run.
