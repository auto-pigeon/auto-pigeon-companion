---
id: aucom.extractor-acquisition
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: The extractor is acquired, never embedded — catalogue, updates and the compatibility manifest
authority:
  - aucom-extractor-separation
  - aucom-acquisition-machinery
  - aucom-compatibility-manifest
topics:
  - extractor
  - aue
  - acquisition
  - download
  - update
  - catalogue
  - manifest
  - signature
  - licence
  - provenance
  - verification
paths:
  - internal/aue/**
  - internal/acquire/**
  - internal/catalog/**
  - catalog/**
---

# The extractor is acquired, never embedded — catalogue, updates and the compatibility manifest

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 55-114 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

## 1. THE EXTRACTOR IS NOT IN THIS BINARY, AND THERE IS NO THIRD WAY TO ONE (`AUE/AUB/AUCOM 211`)

**Auto-Pigeon Extractor is a separate program under a different licence. It is
downloaded against a signed catalogue, at a version a signed compatibility
manifest names, and run as its own process. Nothing here contains it, embeds it,
or claims a licence over it.**

```text
managed             a verified cache entry, at the version the manifest names
developer override  AUCOM_AUE_BINARY, unverified, local, and labelled so
```

There is **no third way and no fallback between the two.** A managed resolution
that fails is an error the user reads; it never quietly becomes an override, and
an override is never quietly treated as verified.
`internal/aue.Provenance.Verified` is the ONE place that distinction is
recorded, it travels with every runner, and every surface that shows an
extractor shows it. A caller that has to compute "was this verified" from four
other fields is a caller that will one day compute it wrong.

### What embedding cost, and why it is three separate faults

`internal/aue/embed.go` copied a platform's extractor binary into this executable
with `//go:embed`. It was never released, and it had to go for three unrelated
reasons — a later change that fixes one of them has not fixed the others:

1. **Licensing.** An MIT artifact contained and appeared to cover an AGPL
   program, and a user had no way to tell whose bytes they were running.
2. **Verification.** Nothing checked the staged binary. `//go:embed` resolves at
   compile time, so a stale or wrong-platform file shipped silently and failed
   on the user's machine.
3. **Coupling.** A patched extractor needed a new Companion release.

CI fails if it returns: `no-embedded-extractor` checks for the directory, for
the directive, and for any committed executable anywhere in the tree.

### It reuses the acquisition machinery; it is not a second updater

`internal/acquire` verifies, downloads, caches and re-checks — the same verifier,
the same sticky revocations, the same serial ratchet, the same cache every other
managed tool uses. This package decides only WHICH version to ask for. A change
to the verification chain therefore applies here with nothing kept in step, and
a prompt that adds a second update path for the extractor is undoing that.

### The compatibility manifest is a THIRD signed document, and it carries no digest

`aucom.compatibility/1.0` maps a Companion version and a platform to a component
version and a minimum protocol. Every fact about the BYTES — size, digest,
signer, licence, source — stays in the catalogue and only there, so there is no
second copy of a digest for a careless edit to make wrong. Two documents that
both carry the digest can disagree; one carries the digest and the other carries
the choice.

**Overlapping rules are refused, not resolved by order.** A rule that depends on
which one a reader's eye reaches first is a rule nobody can review, and a
publisher who splits a range and gets the boundary wrong by one release would
silently install the older build for everybody in the overlap. `Requirement`
therefore returns *the* match rather than the first, and a later change that
introduced a precedence order would have to delete that check first.
