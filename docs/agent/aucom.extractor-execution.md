---
id: aucom.extractor-execution
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: Running the extractor — the handshake, bounded invocation, and what was retired with the downloads
authority:
  - aucom-extractor-handshake
  - aucom-offline-authority
  - aucom-invocation-bounds
  - aucom-authorization-not-verification
  - aucom-publishing-executable
topics:
  - extractor
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
  - internal/aue/**
  - internal/job/**
prerequisites:
  - aucom.extractor-acquisition
---

# Running the extractor — the handshake, bounded invocation, and what was retired with the downloads

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 115-177 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

### The protocol handshake, before the executable is used for anything

A verified executable is not automatically one this build can talk to. **Majors
equal, minor at least the required one** — never `>= major`, because a later
major is defined as breaking. The rule lives in one function on each side of the
boundary (`aue.ProtocolSatisfies` here, `protocol.Satisfies` there) and a
second call site implementing a laxer version of it is the failure mode.

The required protocol is `aue.RequiredProtocol` (`"1.0"`), compiled in. The
bundled extractor is asked `protocol --json` before it is handed to anybody —
whether or not the bundle manifest listed its digest — and refused if the
answer does not satisfy it. A `AUCOM_AUE_BINARY` developer override gets no
handshake; that is part of why it is labelled UNVERIFIED.

Without a required protocol the Companion would be trusting a version NUMBER to
imply a contract, which is exactly the assumption a rebuilt or forked extractor
breaks.

### Offline is a different authority, never a weaker check

**Retired 2026-09-23 with the downloads.** There is no offline mode because
there is no online one: nothing is fetched, so there is nothing to fall back
from. `extractor-pin.json` and the catalogue state are gone; the requirement is
`aue.RequiredProtocol` and the digest is the release's `bundle-manifest.json`.
What survives is the rule underneath: **a check that failed, or could not run,
is a refusal** — an unreadable bundle manifest refuses rather than reading as
"no manifest" (threat row T08), and never becomes "use it anyway".

### Every invocation is bounded, and the bounds are not decoration

A timeout on the WHOLE run, because a process printing one line every nine
minutes keeps a per-read deadline satisfied for ever. **SIGTERM first**, because
the extractor's published contract says a supervised run ends deliberately on
one and writes a record saying why; the grace period is a field, so the default
is the patient production answer and a test can shorten it. A fresh working
directory per invocation, removed afterwards. A bounded read, because a
subprocess is not a trusted producer of unbounded output — and the cap is
reported as the cap, not as whatever the child died of when the pipe closed.

`RunJSON` refuses an empty body and trailing content. A subprocess can exit 0
having printed a warning, half a document, or a document with something
appended, and each of those decodes into a partially-filled struct a caller then
acts on.

### Authorization is not verification

**Retired 2026-09-23.** `acquire.Options.Authorize`, the `Downloader` and
`catalog.RedactURL` were deleted with the download path, and so was AUB's
AUE download grant (`internal/aub/aue.go`). The distinction still binds the
downloads that remain — maps, textures/WADs and join content from AUB
(`internal/assetsync`): **who let you fetch the bytes and whether the bytes are
the right bytes are different questions with different answers.** A backend
authorizing a fetch never makes this program run what it fetched, and no
download path may be added that ends in an executable (threat row T05).

### Publishing is executable, and holds no key here

**Retired 2026-09-23.** `companion catalog release` and `companion catalog sign`
are gone with the catalogue; there is nothing for this program to publish or
sign about the extractor. Publishing the extractor is now an act of the RELEASE:
`build/bundle-sidecar.sh --extractor …` puts a prebuilt file beside the
Companion and `build/bundle-manifest.py` lists its digest, licence and
corresponding source. **CI still reads no secrets**, and that is exactly why the
release workflow does not build AUE yet: the AUE repository is private, and
reading it would need a token — deferred until the repositories move to the
`auto-pigeon` organisation.
