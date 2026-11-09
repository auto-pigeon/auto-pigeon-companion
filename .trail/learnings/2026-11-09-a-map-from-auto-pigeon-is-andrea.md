---
title: "A map from Auto-Pigeon is checked by the bundled converter's canonical-map, not by code linked into the Companion"
author: Andrea-dintino
date: 2026-11-09
branch: main
cost: { minutes: 57, tokens: 577249 }
id: l-24ee5663
scope: repo
status: active
src: agent
touches:
  - internal/build/mapconfine.go
  - internal/build/runner.go
  - internal/build/mapconfine_test.go
  - internal/build/source_test.go
  - internal/build/mapconfine_compile_test.go
  - internal/threat/rows.go
  - docs/agent/aucom.extractor-acquisition.md
  - build/converter.json
---

## Decided
The compile boundary for AUB-sourced maps (NEW_434) runs in the bundled converter (`auto-pigeon-mapio canonical-map`, protocol 1.1, capability `compile_boundary: canonical-map/1`), and the Companion calls it from `Runner.stageInputs` through `build.Options.CanonicalMap`. No converter means an AUB map is refused, never compiled as downloaded.

Why: `docs/agent/aucom.extractor-acquisition.md` freezes "Only cmd/mapio imports go/mapio" and "the Companion links no external module" (MIT-only binary, Apache-2.0 notices travel only with the converter, `companion security audit` reports no module deps). go/mapio's reader is the canonical parser the prompt asked for, so the check must live where go/mapio is linked.

## Tried and ruled out
- Parsing in the Companion with go/mapio/mapfile + quake3map: works, but breaks the frozen rule; `cmd/companion` TestTheConverterChecksRefuseWhatTheReleaseCouldNotStandBehind fails because it uses the Companion binary as "a program that does not link the library".
- go/mapio/mapfile's own parser: refuses Quake fence textures (`{fence` is split into a brace and a word). quake3map (Q3Map2's tokenizer) reads every Q1/Q2/Q3 map here, fence textures included.
- Expecting byte-identical BSPs from ericw 1.4556: multi-threaded qbsp is not reproducible at all; single-threaded qbsp output depends on source LINE numbers and even on whitespace (brushes ordered by (chop_index, line_number); the marksurfaces order moves with layout). Compare with -threads 1, against the comment-stripped original, or lump-by-lump with leaf face sets.

## Watch out
- Test fixtures that signal a fake compiler through `//` comments in an AUB map break: the canonical copy carries no comments. Use worldspawn keys (`_aucom_test_*`, see internal/web harness_test.go fixtureValues).
- A CLI test that builds an `aub:` `.map` now needs a converter: `useRealConverter(t)` in internal/cli.

In the code: cmd/mapio/canonical.go (rule), internal/build/mapconfine.go (staging), internal/converter/canonical.go, build/converter.json required_protocol 1.1.
