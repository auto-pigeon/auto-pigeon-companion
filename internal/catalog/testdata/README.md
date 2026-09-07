# Test fixtures — signing keys

Every key in this directory was generated for this repository's tests and for
nothing else. They are committed on purpose: a signing path whose only test
generates its own keys at run time never exercises reading a key from disk, and
the private-key file format is part of the operational procedure.

**None of them signs anything outside `go test`.** No catalogue this project
publishes is signed by them, no build trusts them unless a test hands them to it
as an anchor file, and there is no production catalogue key in this repository at
all — see `README.md` for how a real one is created and kept.

- `anchor-1.key.json` — an anchor key. Signs the keyring.
- `catalog-1.key.json`, `catalog-2.key.json` — catalogue signing keys. The
  second exists so key rotation and revocation have a second key to rotate to.
- `anchors.json` — the trust anchor file naming `anchor-1`'s public half.

## `example-release-manifest.json`

The shape `auto-pigeon-extractor`'s `scripts/build-release.sh` writes, with
**fabricated digests** — `sha256:000…001` and so on, deliberately obvious — and
version `0.0.0-example`.

It is not a release and nothing may publish it. It exists so that
`companion catalog release` can be exercised end to end without a build of that
program and without anything being signed: CI composes a catalogue package and a
compatibility component from it and checks they validate. Signing is covered by
tests that generate their own keys.
