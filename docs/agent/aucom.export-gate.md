---
id: aucom.export-gate
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: The export gate is the only way out, and trust does not travel in
authority:
  - aucom-export-gate
  - aucom-trust-does-not-travel
topics:
  - publish
  - export
  - profile
  - portable
  - trust
  - community
  - install
  - confirmation
  - deployment
  - catalogue
paths:
  - internal/publish/**
  - internal/profile/**
---

# The export gate is the only way out, and trust does not travel in

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 178-229 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

## 2. THE EXPORT GATE IS THE ONLY WAY OUT, AND TRUST DOES NOT TRAVEL IN (`AUB/AUP/AUG/AUCOM 213`)

`AUCOM 204` defined the portable profile and `212` made one runnable. `20260906_213` made one
publishable. `internal/publish` owns both directions; the README's *Publishing a profile, and taking
somebody else's* section is the user-facing statement of them. Six things follow, and they bind every
future prompt.

1. **THERE IS ONE FUNCTION THAT PRODUCES BYTES TO PUBLISH, AND IT REFUSES.** `publish.PreviewOf`
   calls `profile.Export`, which validates and canonicalizes, and canonicalization runs
   `CheckPortable`. So a document naming an absolute path, a home directory, a network address or
   anything shaped like a credential cannot be PREVIEWED — let alone published. That is what makes
   "publishing a local binding is structurally impossible" a property of the code rather than a rule
   somebody remembers. **Never add a second path to publishable bytes**, and never add a `--force`,
   an `--allow-local` or a "publish this document as-is".

2. **PUBLISHING NEEDS AN EXPLICIT CONFIRMATION AND INSTALLING NEEDS AN EXPLICIT APPROVAL.** Both are
   parameters, not defaults: `Publish` refuses `confirmed: false` and `Apply` refuses
   `approved: false` before writing ANYTHING — not even the document, because a document on disk is
   one the local catalog lists, and listing something nobody agreed to is how a review becomes a
   formality.

3. **A DEPLOYMENT'S TRUST STATE NEVER BECOMES THIS MACHINE'S.** An installed profile is
   `profile.TrustCommunity`, always. AUB's `builtin` is the ONE state `profile.Authorize` accepts
   with no grant, and AUB's `verified` claims a catalogue signature this build did not check;
   adopting either would let anybody who runs an AUB hand out an unreviewed run.
   `TestADeploymentCannotHandOutTrustThisMachineDidNotCheck` fails a change that does. The badge is
   carried as `Plan.DeploymentTrust` and shown, because it is real information about what somebody
   with a stake in that deployment thinks — it is just not this machine's authorization.

4. **THE DIGEST IS RECOMPUTED HERE AND THE CANONICAL FORM IS CHECKED HERE.** AUB deliberately does
   not re-canonicalize, because RFC 8785 is this repository's algorithm; so this is the side that
   proves it, by re-exporting the decoded document and comparing byte-for-byte. Never accept the
   announced digest, and never install bytes that are not the canonical encoding of what they decode
   to — a digest over a non-canonical encoding names bytes nobody else would produce for the same
   document.

5. **THE PORTABILITY CORPUS IS SHARED AND PINNED.** `internal/profile/testdata/portability-corpus.json`
   is byte-identical to auto-pigeon-backend's copy and both repositories pin its SHA-256, because
   neither can read the other's tree at test time. Changing what may never be published is a change
   in two repositories, in one task, or in neither.

6. **A WITHDRAWN VERSION IS INSTALLABLE, AND NEVER SILENTLY.** Reproducing a build that used one is a
   legitimate reason to want it. The publisher's reason and any replacement they named travel with
   the plan and are printed before an approval. Never refuse one, and never install one without
   printing why it was withdrawn.

**What must not be done to make something pass.** Do not add a flag that skips the preview or the
approval. Do not map AUB's trust word onto `profile.Trust`. Do not trust an announced digest. Do not
publish anything but `profile.Export`'s output. Do not send a `trust`, `moderation_state`, `verified`
or `publisher` member in a publication — they do not exist in the request and this client must not
grow them.
