---
title: "NEW_371: the Companion's converter is auto-pigeon-mapio, built here from go/mapio"
author: Andrea-dintino
date: 2026-10-30
branch: main
id: l-4e853aa7
scope: repo
status: active
src: agent
touches:
  - build/release-acceptance.py
  - internal/web/guard_test.go
  - go.mod
---

## Decided
- The Companion's map converter is auto-pigeon-mapio: cmd/mapio here plus go/mapio from auto-pigeon-libraries at the revision go.mod pins. Only cmd/mapio imports the library, so the Companion binary still links no external module.
- Manifests went to /2.0 on purpose: a Companion that bundled the extractor refuses the new shape and its user downloads once by hand. Accepting it would need a manifest calling the converter the extractor.
- internal/converter's handshake checks the program's own name first, because the extractor also speaks a protocol 1.x.

## Tried and ruled out
- Vendoring the library so CI could run without AULIBS being pushed: it would publish the code through another route and is a second copy.

## Watch out
- The pin only resolves on GitHub once AULIBS main is pushed. Push AULIBS, then AUE, then AUCOM.
- `companion build run` cannot be given tool_root by flag; it comes from a granted tool binding, so a scratch HOME cannot drive a compile without one.
- `git add -u` sweeps in trail's own self-upgrade of .claude/settings.json and .trail/ when trail has just run. Check the staged list.
