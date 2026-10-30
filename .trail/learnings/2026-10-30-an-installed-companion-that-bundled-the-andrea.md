---
title: "An installed Companion that bundled the extractor crosses to MapIO only through a migration bridge, proved on the released binary"
author: Andrea-dintino
date: 2026-10-30
branch: main
cost: { minutes: 51, tokens: 666641 }
id: l-94b28c02
scope: repo
status: active
src: agent
touches:
  - build/migration-bridge.json
  - build/migration-bridge.py
  - cmd/companion/migration_bridge_test.go
  - internal/cli/selfupdate_cmd.go
  - internal/converter/resolve.go
  - internal/converter/resolve_test.go
  - internal/managedtools/self.go
  - internal/managedtools/self_test.go
  - internal/web/selfupdate.go
  - internal/converter/runner.go
---

## Decided
- No reader change reaches an installed program, so "the new reader accepts the old" is meaningless for v1.323..v1.336. They read release-manifest/1.0 and bundle-manifest/1.2 only.
- A migration bridge is a separately classified artifact: this Companion plus converter in the older shape, with the historical extractor pinned by digest, each under its own name and licence. Publishing one is the operator's decision (it republishes a proprietary executable); the manual route stays valid.
- The older shape is read only about the bundle a process runs from, only with `migration_bridge`, never as a download. Nothing updates TO a bridge.

## Tried and ruled out
- Calling the converter the extractor, or lowering a schema string: false record, and the prompt forbids it.
- The aucom_fixture_releases seam to test the old updater: that is a rebuild, not the released program. HTTPS_PROXY plus SSL_CERT_FILE reach a real released binary unchanged, with a local TLS stand-in that serves real bytes.

## Watch out
- Three things the old binary does decide what a bridge must be, and none is visible from the new code: its Prove needs extractor_verified; it marks executable only programs its schema names, so MapIO lands 0644 and Resolver restores the bit; it reads maxManifestsPerCheck (6) manifests newest first, so a bridge goes unreachable after six higher releases.
- Versions are strictly N.M and build/release.sh ties them to commit count, so a bridge needs an unused number in a gap between releases, and programs stamped by hand.
- A manual `tools check` within a minute of the last one is refused: live scenarios need 62-second gaps.

In the code: internal/managedtools/self.go parseOwnBundle, internal/converter/resolve.go restoreBridgeMode, internal/cli/selfupdate_cmd.go selfUpdateProve, build/migration-bridge.py, build/migration-bridge.json. Rule 13 of docs/agent/aucom.managed-tools.md.
