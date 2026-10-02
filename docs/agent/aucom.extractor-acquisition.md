---
id: aucom.extractor-acquisition
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: The extractor ships beside the Companion — never embedded, never downloaded
authority:
  - aucom-extractor-separation
  - aucom-acquisition-machinery
  - aucom-compatibility-manifest
topics:
  - extractor
  - aue
  - acquisition
  - bundle
  - bundled
  - release
  - manifest
  - download
  - licence
  - provenance
  - verification
paths:
  - internal/aue/**
  - internal/acquire/**
  - build/bundle-sidecar.sh
  - build/bundle-manifest.py
---

# The extractor ships beside the Companion — never embedded, never downloaded

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 55-114 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

<!-- Rewritten 2026-09-23 by operator decision: "AUE binary should be (will be) built in github CI of AUCOM, inserted in AUCOM and this will appear in the github releases ... So all the download mechanisms should disappear." The heading lines below are kept verbatim so `sections` still resolves them; the prose under two of them now records what was retired and why. ADR 0004 is superseded — see docs/adr/. -->

## 1. THE EXTRACTOR IS NOT IN THIS BINARY, AND THERE IS NO THIRD WAY TO ONE (`AUE/AUB/AUCOM 211`)

**Auto-Pigeon Extractor is a separate program under a different licence
(proprietary — `LicenseRef-Auto-Pigeon-Proprietary`, Copyright (c) 2026 Andrea
D'Intino, all rights reserved, since `NEW_247G`; this repository is MIT). It
ships BESIDE the Companion, as its own file in the release bundle, named with
its own licence in the bundle manifest, and runs as its own process. Nothing here contains it, embeds it,
downloads it, or claims a licence over it.**

Licence rules for the extractor, since `NEW_247G` (2026-09-24):

- **Quote, never restate.** What an archive says about the extractor's licence
  is quoted from the pinned build: its release manifest's `license.spdx`
  (`build/release-plan.py check-aue`). Since 2026-09-25 no licence or notice
  file ships inside an archive (operator decision); the extractor's licence
  text is `LICENSE` in its own repository. The compiled-in component list
  (`internal/release.Extractor`, shown by `companion security audit` and
  `companion release sbom`) states the current policy,
  `LicenseRef-Auto-Pigeon-Proprietary`.
- **Never MIT, never listed as copyleft.** An extractor declared MIT is refused
  by `bundle-manifest.py` and `release-plan.py`; an archive listing it so would
  read as entirely MIT. A proprietary extractor offers no corresponding source
  and none is demanded — that obligation is copyleft's, and the copyleft checks
  stay copyleft checks.
- **Older AGPL-3.0-only builds are still accepted** by the handshake and the
  bundle scripts, and their copies keep that licence.
- **Using the bundled extractor needs the copyright owner's written
  authorization**, per its licence. Nothing here grants it, and no document here
  may imply that it does.

```text
bundled             dependencies/auto-pigeon-extractor[.exe] beside the
                    Companion's own executable (inside a macOS .app:
                    Contents/MacOS/, beside it); digest-checked when the
                    bundle manifest lists it; must pass the protocol handshake
developer override  AUCOM_AUE_BINARY, unverified, local, no handshake, and
                    labelled UNVERIFIED everywhere it is shown
```

There is **no third way and no fallback between the two.** A bundled
resolution that fails is an error the user reads; it never quietly becomes an
override, and an override is never quietly treated as verified. When nothing is
beside the Companion the error says so — *no Auto-Pigeon Extractor was shipped
beside this Companion … a development build can name one with AUCOM_AUE_BINARY*
— and nothing goes and fetches one.

The bundled file's verification, in `internal/aue.Resolver`:

- `bundle-manifest.json` beside it lists `dependencies/auto-pigeon-extractor[.exe]` with a
  SHA-256 → the bytes must hash to it. Match: **verified**. Mismatch: **refused**
  ("it is not the extractor this release shipped, and it is not run").
- no manifest, or the manifest does not list it → it runs **unverified**, with
  `aue.UnlistedNote` (nothing checked its digest; it still had to pass the
  protocol check).
- a manifest that cannot be read → **refused**, never treated as absent.

`internal/aue.Provenance.Verified` is the ONE place that distinction is
recorded, it travels with every runner, and every surface that shows an
extractor shows it. A caller that has to compute "was this verified" from four
other fields is a caller that will one day compute it wrong.

**Where the file comes from.** `build/bundle-sidecar.sh --extractor <prebuilt
AUE file> --extractor-version <v> --extractor-source <url>` copies a PREBUILT
extractor into the release archive and `build/bundle-manifest.py` lists it with
its digest and licence. Without `--extractor` the manifest says
`extractor: null` and `extractor_absent`. Nothing in the build downloads it
either. Since `NEW_247A`, `.github/workflows/release.yml` builds AUE on every
push to main from the head of the ONE branch `build/aue-pin.json` names — resolved to one commit by
`release-plan.py aue-checkout`, which every later step names instead of the
branch (operator, 2026-10-02: *"AUCOM should always build with the latest AUE"*;
a commit written in that file is how v1.192 bundled an extractor that could not
read the APMap 1.5 maps the editor was saving) — with AUE's own
`scripts/build-release.sh`, and bundles it; the bundle step reads both
programs' executable headers and refuses a pair built for different machines.
On macOS the extractor goes inside the app, in `Contents/MacOS/`, and the
manifest goes in `Contents/Resources/` (`aue.bundleManifestFor`), because that
is where a Companion in a `.app` looks. The rules for the release itself are
in `aucom.release-verification`; shared ADR-0029 is the cross-repository
record.

### What embedding cost, and why it is three separate faults

`internal/aue/embed.go` copied a platform's extractor binary into this executable
with `//go:embed`. It was never released, and it had to go for three unrelated
reasons — a later change that fixes one of them has not fixed the others:

1. **Licensing.** An MIT artifact contained and appeared to cover a separately
   licensed program (AGPL-3.0 at the time; proprietary now), and a user had no
   way to tell whose bytes they were running.
2. **Verification.** Nothing checked the staged binary. `//go:embed` resolves at
   compile time, so a stale or wrong-platform file shipped silently and failed
   on the user's machine.
3. **Coupling.** A patched extractor needed a new Companion release.

Shipping it beside, as its own file, answers the first two: the licence is its
own and listed in the bundle manifest and `THIRD_PARTY_NOTICES.md`, and the
digest is checked at run time. The third is accepted — a patched extractor now
ships in a new release — and that is the operator's decision, not an oversight.

CI fails if embedding returns: `no-embedded-extractor` checks for the directory,
for the directive, and for any committed executable anywhere in the tree.

### It reuses the acquisition machinery; it is not a second updater

**Retired 2026-09-23. There is no acquisition machinery any more, and no
updater of any kind.** The signed catalogue, keyring, trust anchors, serial
ratchet, revocations, managed downloads, tool cache, GC and licence acceptance
(`internal/catalog`, the root `catalog/` directory, most of `internal/acquire`,
`internal/aub/aue.go`, the `/api/v1/profiles/{id}/acquire` routes and
`companion catalog …`) were deleted.

What remains of `internal/acquire` only FINDS executables — `user_path`,
`system_path`, `already_installed` — and downloads nothing. A profile's
`managed_download` route and its `catalog_package` are LEGACY: still read, so
older and published documents load, never offered and never taken
(`acquire.ErrNoDownloads`). The retired `companion acquire plan|install|accept|
list|verify|use|gc` and `companion extractor plan|install` subcommands print that
they were removed and exit 2. **A prompt that adds a download path — for the
extractor or for any toolchain — is undoing an operator decision**, and has to
say so and name it.

### The compatibility manifest is a THIRD signed document, and it carries no digest

**Retired 2026-09-23 with the catalogue.** There is no compatibility manifest,
no `extractor-pin.json` and no per-platform version choice to make: the
extractor this Companion runs is the one its release shipped. What survives is
the part that was never about downloading — the digest lives in ONE document
(the release's `bundle-manifest.json`), and the contract lives in ONE constant
(`aue.RequiredProtocol`, compiled in, checked by the handshake in
`aucom.extractor-execution`). Do not add a second copy of either.
