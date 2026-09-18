---
id: aucom.extractor-execution
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: Running the extractor — the handshake, offline authority, bounded invocation and what authorization is not
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

# Running the extractor — the handshake, offline authority, bounded invocation and what authorization is not

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 115-177 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

### The protocol handshake, before the executable is used for anything

A verified executable is not automatically one this build can talk to. **Majors
equal, minor at least the required one** — never `>= major`, because a later
major is defined as breaking. The rule lives in one function on each side of the
boundary (`catalog.ProtocolSatisfies` here, `protocol.Satisfies` there) and a
second call site implementing a laxer version of it is the failure mode.

Without `min_protocol` the Companion would be trusting a version NUMBER to imply
a contract, which is exactly the assumption a rebuilt or forked extractor breaks.

### Offline is a different authority, never a weaker check

`extractor-pin.json` records the requirement this machine last VERIFIED, beside
`config.json` with the catalogue state because it records a decision and
clearing a cache must not erase one. Offline resolution reads it and the
handshake still runs against the minimum it carries.

**The fallback happens only because the caller said `Offline`.** A verification
that failed, a rollback attempt, an expired document or an unreachable server is
a refusal and never becomes "use the older answer" — that is the difference
between an offline mode and a way around the catalogue.

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

When the artifact is served by a backend that authorizes downloads,
`acquire.Options.Authorize` rewrites the URL immediately before the fetch.
Nothing else changes: the size, the digest and the signature chain are checked
exactly as they are for a public URL. **Who let you fetch the bytes and whether
the bytes are the right bytes are different questions with different answers**,
and keeping them apart is what stops a compromised backend from being able to
make this program run something.

`Downloader` still holds no credentials and sends none. What the hook returns is
a capability for one artifact valid for minutes — the pre-signed-URL shape
`catalog.RedactURL` already exists to keep out of logs and records.

### Publishing is executable, and holds no key here

`companion catalog release` turns a component's release manifest into the two
unsigned documents, validated against the rules a signature would otherwise make
permanent — including the copyleft rule that refuses a package offering no
corresponding source. `companion catalog sign` is a separate step, so a
publisher reads what they are about to vouch for. **CI reads no secrets**, and a
job of its own keeps it that way: a workflow that could sign from a pull request
would be a workflow that publishes whatever a pull request contains.
