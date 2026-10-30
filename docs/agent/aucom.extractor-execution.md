---
id: aucom.extractor-execution
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: Running the map converter — the handshake, bounded invocation, and what was retired with the downloads
authority:
  - aucom-extractor-handshake
  - aucom-offline-authority
  - aucom-invocation-bounds
  - aucom-authorization-not-verification
  - aucom-publishing-executable
topics:
  - extractor
  - converter
  - mapio
  - aue
  - handshake
  - protocol
  - offline
  - bounds
  - timeout
  - authorization
  - verification
  - execution
paths:
  - internal/converter/**
  - internal/job/**
  - cmd/mapio/**
prerequisites:
  - aucom.extractor-acquisition
---

# Running the map converter — the handshake, bounded invocation, and what was retired with the downloads

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 115-177 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

<!-- Rewritten 2026-10-30 by NEW_371: the guarantees stand, adapted to the converter's identity. Heading lines are kept verbatim so `sections` still resolves them. -->

### The protocol handshake, before the executable is used for anything

A verified executable is not automatically one this build can talk to. Three questions, in this order,
and a wrong answer to any is a refusal:

1. **Who is this?** `protocol --json` must say `"program": "auto-pigeon-mapio"`. Protocol numbers alone
   prove nothing: the extractor also speaks a protocol 1.x, and a full extractor placed at this path must
   not pass for a converter.
2. **Do I speak its contract?** **Majors equal, minor at least the required one** — never `>= major`,
   because a later major is defined as breaking. The rule lives in one function on each side of the
   boundary (`converter.ProtocolSatisfies` here, `protocol_satisfies` in `build/release-plan.py`) and a
   third call site implementing a laxer version of it is the failure mode.
3. **Can it do what a build needs?** Its capabilities must list every direction in
   `converter.RequiredDirections` and name the APMap versions it reads and writes.

The required protocol is `converter.RequiredProtocol` (`"1.0"`), compiled in. The bundled converter is
asked before it is handed to anybody — whether or not the bundle manifest listed its digest. A
`AUCOM_MAPIO_BINARY` developer override gets no handshake; that is part of why it is labelled UNVERIFIED.

Without a required protocol the Companion would be trusting a version NUMBER to imply a contract, which
is exactly the assumption a rebuilt or forked converter breaks.

`cmd/mapio`'s contract is its protocol document: `version`, `protocol --json` and
`convert --<direction> --input <file> --output <dir> [--json]`. Exit 0 wrote the output, 1 did not, 2 was
a wrong invocation. A failed conversion's last stderr line is `MAPIO-TERMINAL/1.0 {reason, message, detail}`,
and its reasons are `go/mapio`'s refusal codes plus `io_error`. It validates always and takes no flag that
turns validation off.

**A result is published as one, and what that means is exact (`NEW_371A`, `cmd/mapio/commit.go`).** A
Quake III result is two files, and renaming each atomically does not make the pair atomic: at `1229f08`,
158 of 200 refused commits left a `.map` published with no manifest. Now:

- **exit 0**: every file is at its path, read back and matching; a manifest describes the bytes beside it.
- **a returned I/O error or a cancellation at any boundary**: what stood at those paths stands there again,
  byte for byte, and unrelated files in `--output` are untouched. A rollback that cannot finish says so and
  names the `.mapio-commit-*` folder holding the previous files.
- **process death**: there is NO cross-file atomicity, and none is claimed. The write-ahead journal in
  `.mapio-commit-*` is what the next conversion into that directory undoes, before anything else; until
  then the manifest is published last and names the `.map` by digest, so a reader refuses the state
  (`assetref.conversionManifest`, and a Quake III `.map` with no manifest is refused outright). Killed at
  every boundary in `TestAProcessKilledAtAnyStepIsUndoneByTheNextRun`.
- **power loss** is not promised: the flush order is an argument, not a test, and Windows cannot flush a
  directory.

The commit point is one step, removing the journal, and a cancellation that arrives after it changes
nothing because the result is whole. `.mapio-commit.lock` stays in the output directory on purpose: it
serialises conversions sharing a directory (bounded wait, then refused), and removing a lock file is how
two processes end up holding two locks (`internal/oslock`). A directory whose file system has no lock is
refused. `--output` itself is never replaced or emptied.

### Offline is a different authority, never a weaker check

**Retired 2026-09-23 with the downloads.** There is no offline mode because there is no online one:
nothing is fetched, so there is nothing to fall back from. The requirement is
`converter.RequiredProtocol` and the digest is the release's `bundle-manifest.json`. What survives is the
rule underneath: **a check that failed, or could not run, is a refusal** — an unreadable bundle manifest
refuses rather than reading as "no manifest" (threat row T08), and never becomes "use it anyway".

### Every invocation is bounded, and the bounds are not decoration

A timeout on the WHOLE run, because a process printing one line every nine minutes keeps a per-read
deadline satisfied for ever. **SIGTERM first**, because `cmd/mapio` turns it into a cancelled context and
ends with a record saying why; the grace period is a field, so the default is the patient production
answer and a test can shorten it. On Windows, where a process cannot be sent SIGTERM, the signal fails and
the child is killed when the grace period ends. A fresh working directory per invocation, removed
afterwards. A bounded read, because a subprocess is not a trusted producer of unbounded output — and the
cap is reported as the cap, not as whatever the child died of when the pipe closed.

`RunJSON` refuses an empty body and trailing content. A subprocess can exit 0 having printed a warning,
half a document, or a document with something appended, and each of those decodes into a partially-filled
struct a caller then acts on.

The converter is resolved lazily (`converter.LazyRunner`), so a server that never converts a map never
hashes or starts it.

### Authorization is not verification

**Retired 2026-09-23.** `acquire.Options.Authorize`, the `Downloader` and `catalog.RedactURL` were deleted
with the download path, and so was AUB's extractor download grant (`internal/aub/aue.go`). The distinction
still binds the downloads that remain — maps, textures/WADs and join content from AUB
(`internal/assetsync`): **who let you fetch the bytes and whether the bytes are the right bytes are
different questions with different answers.** A backend authorizing a fetch never makes this program run
what it fetched, and no download path may be added that ends in an executable (threat row T05).

### Publishing is executable, and holds no key here

**Retired 2026-09-23.** `companion catalog release` and `companion catalog sign` are gone with the
catalogue. Publishing the converter is an act of the RELEASE: `build/bundle-sidecar.sh --converter …` puts
a built file beside the Companion and `build/bundle-manifest.py` lists its digest, its licences and the
commits it came from. **CI reads no secrets** for any of it: everything a release builds from is this
repository and a public module, and `AUE_CHECKOUT_TOKEN` is no longer read by any workflow. The repository
secret itself is the operator's to delete.

### How a build converts an APMap (moved from README.md, 2026-10-17)

A map in the account is an APMap and a compiler reads a `.map`, so a build converts first, choosing its
direction by the APMap's `game`: `quake1` → `--apmap-to-q1map`, `quake2` → `--apmap-to-q2map`, `quake3` →
`--apmap-to-q3map`. The Quake III direction also writes `<name>.q3map-manifest.json` beside the `.map` in
the same `converted-<name>/` directory — the source document's identity, every shader, model and sound the
map names, and every conversion warning — and that manifest is refused if its digests are not those of the
two files in hand. A converter refusal (a patch or shader it will not write) is reported in its own words
and nothing is built. A local `.apmap` given to `build run` is copied into the build's temporary stage
first, so nothing is written beside the user's file; `companion converter convert` runs the same path by
hand. Build & Run and ordinary builds share this one path, `assetref.ConvertAPMapInputs`, and a build's
record names the converter, its version and the go/mapio revision.
