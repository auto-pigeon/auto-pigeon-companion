---
id: aucom.extractor-acquisition
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: The map converter ships beside the Companion — never embedded, never downloaded
authority:
  - aucom-extractor-separation
  - aucom-acquisition-machinery
  - aucom-compatibility-manifest
topics:
  - extractor
  - converter
  - mapio
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
  - internal/converter/**
  - internal/acquire/**
  - cmd/mapio/**
  - build/bundle-sidecar.sh
  - build/bundle-manifest.py
  - build/converter.json
---

# The map converter ships beside the Companion — never embedded, never downloaded

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 55-114 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

<!-- Rewritten 2026-10-30 by NEW_371, by operator decision: map conversion moved out of the proprietary extractor into the Apache-2.0 go/mapio library, and "the previous instruction to build the latest AUE for every AUCOM release is superseded for this converter path". The heading lines below are kept verbatim so `sections` still resolves them; the prose under them states the current architecture. ADR-0011 is the record. -->

## 1. THE EXTRACTOR IS NOT IN THIS BINARY, AND THERE IS NO THIRD WAY TO ONE (`AUE/AUB/AUCOM 211`)

**The Companion no longer runs Auto-Pigeon Extractor at all. It runs `auto-pigeon-mapio`, a small
converter built in this repository, and that program is not in the Companion's binary either: it ships
BESIDE the Companion as its own file in the release bundle, named with its licences in the bundle
manifest, and runs as its own process.**

| what | where it lives | licence |
| --- | --- | --- |
| the format code | `go/mapio` in the public `auto-pigeon-libraries` | Apache-2.0 |
| the executable wrapper | `cmd/mapio` here | MIT |
| the executable built from both | `dependencies/auto-pigeon-mapio[.exe]` | `Apache-2.0 AND MIT` |
| the Companion | `cmd/companion` here | MIT, and it links no external module |

Only `cmd/mapio` imports `go/mapio`. `companion security audit` still reports no Go module dependencies,
because the Companion's own binary has none; the SBOM names the converter and the library revision.

Licence rules for the converter:

- **Both licences, always.** MIT alone is refused by `bundle-manifest.py` and `release-plan.py`: an
  archive listing it so would read as entirely MIT. The wrapper does not relicense the library, and the
  library's licence does not cover the wrapper.
- **The notices travel with it.** `NOTICES-auto-pigeon-mapio.txt` beside the converter (in
  `Contents/Resources/` on macOS) carries the wrapper's MIT text and the library's Apache-2.0 text and
  NOTICE, taken at build time from this commit and the exact module revision. This is the one licence
  text an archive carries: the 2026-09-25 decision that no licence or notice file ships in an archive
  still holds for everything else, and is narrowed here because the library is redistributed in binary
  form and Apache-2.0 requires its text to go with it.
- **It is never called the extractor.** It identifies as `auto-pigeon-mapio`, and a build records that.
  A build or Play record from before this change says `extractor`, and stays that way: it is history.

```text
bundled             dependencies/auto-pigeon-mapio[.exe] beside the Companion's
                    own executable (inside a macOS .app: Contents/MacOS/, beside
                    it); digest-checked when the bundle manifest lists it; must
                    pass the protocol handshake
developer override  AUCOM_MAPIO_BINARY, unverified, local, no handshake, and
                    labelled UNVERIFIED everywhere it is shown
```

There is **no third way and no fallback between the two.** A bundled resolution that fails is an error
the user reads; it never quietly becomes an override, and an override is never quietly treated as
verified. When nothing is beside the Companion the error says so — *no converter was shipped beside this
Companion … a development build can name one with AUCOM_MAPIO_BINARY* — and nothing goes and fetches one.

**`AUCOM_AUE_BINARY` and `AUC_AUE_BINARY` are retired.** They are never read as a path: a full extractor
is a different program under a different licence, and it would fail the handshake anyway. Setting one
earns a message saying so (`converter.RetiredOverrideNote`) and changes nothing about what runs.
`companion extractor …` remains as an alias of `companion converter …`, and runs the same converter.

The bundled file's verification, in `internal/converter.Resolver`:

- `bundle-manifest.json` beside it lists `dependencies/auto-pigeon-mapio[.exe]` with a SHA-256 → the
  bytes must hash to it. Match: **verified**. Mismatch: **refused** ("it is not the converter this release
  shipped, and it is not run").
- no manifest, or the manifest does not list it → it runs **unverified**, with `converter.UnlistedNote`
  (nothing checked its digest; it still had to pass the protocol check).
- a manifest that cannot be read → **refused**, never treated as absent.

`internal/converter.Provenance.Verified` is the ONE place that distinction is recorded, it travels with
every runner, and every surface that shows a converter shows it. A caller that has to compute "was this
verified" from four other fields is a caller that will one day compute it wrong.

**Where the file comes from.** `build/release.sh` builds `cmd/mapio` for every target the Companion is
built for, with the same constraints (`CGO_ENABLED=0 -trimpath`), against the go/mapio revision `go.mod`
pins. `build/bundle-sidecar.sh --converter <file> …` copies it into the archive and
`build/bundle-manifest.py` lists it with its digest, licences and library revision; without
`--converter` the manifest says `converter: null` and `converter_absent`. No other repository's checkout
and no credential is involved, so anybody can build a release from a clone. The bundle step reads both
programs' executable headers and refuses a pair built for different machines. On macOS the converter
goes inside the app, in `Contents/MacOS/`, and the manifest and notices go in `Contents/Resources/`
(`converter.bundleManifestFor`), because that is where a Companion in a `.app` looks. The rules for the
release itself are in `aucom.release-verification`; ADR-0011 and shared ADR-0046 are the records.

**A converter fix ships in a new Companion bundle, and only there.** Nothing updates the converter on
its own: `internal/selfupdate` replaces a complete, verified bundle or nothing (`aucom.managed-tools`).

### What embedding cost, and why it is three separate faults

`internal/aue/embed.go` once copied a platform's extractor binary into this executable with `//go:embed`.
It was never released, and it had to go for three unrelated reasons, and they still apply to the
converter — a later change that fixes one of them has not fixed the others:

1. **Licensing.** An MIT artifact would contain and appear to cover a separately licensed program, and a
   user would have no way to tell whose bytes they were running.
2. **Verification.** `//go:embed` resolves at compile time, so a stale or wrong-platform file ships
   silently and fails on the user's machine.
3. **Coupling.** A patched converter would need a rebuilt Companion binary rather than a rebuilt bundle.

CI fails if embedding returns: `no-embedded-converter` checks for the directory, for the directive, and
for any committed executable anywhere in the tree.

### It reuses the acquisition machinery; it is not a second updater

**Retired 2026-09-23. There is no acquisition machinery any more, and no updater of any kind for this
program.** The signed catalogue, keyring, trust anchors, serial ratchet, revocations, managed downloads,
tool cache, GC and licence acceptance (`internal/catalog`, the root `catalog/` directory, most of
`internal/acquire`, `internal/aub/aue.go`, the `/api/v1/profiles/{id}/acquire` routes and
`companion catalog …`) were deleted.

What remains of `internal/acquire` only FINDS executables — `user_path`, `system_path`,
`already_installed` — and downloads nothing. A profile's `managed_download` route and its
`catalog_package` are LEGACY: still read, so older and published documents load, never offered and never
taken (`acquire.ErrNoDownloads`). The retired `companion acquire plan|install|accept|list|verify|use|gc`
and `companion converter plan|install` subcommands print that they were removed and exit 2. **A prompt that
adds a download path — for the converter or for any toolchain — is undoing an operator decision**, and has
to say so and name it. One such decision exists: HITL 2026-10-28 (`NEW_367`, ADR-0010) authorised exactly
the two Auto-Pigeon toolchains, through `internal/managedtools` and nothing here — `aucom.managed-tools`
owns its rules. `NEW_368` (same decision, ADR-0010's amendment) added the Companion's own release bundle
as the second kind of official target: the bundled converter arrives only as part of a complete, verified
Companion bundle whose bundle manifest pairs them, never on its own, and a developer override
(`AUCOM_MAPIO_BINARY`) is never taken for the bundled one in a candidate's proving start.

### The compatibility manifest is a THIRD signed document, and it carries no digest

**Retired 2026-09-23 with the catalogue.** There is no compatibility manifest and no per-platform version
choice to make: the converter this Companion runs is the one its release built. What survives is the part
that was never about downloading — the digest lives in ONE document (the release's
`bundle-manifest.json`), the contract lives in ONE constant (`converter.RequiredProtocol`, compiled in,
checked by the handshake in `aucom.extractor-execution`), and the library revision lives in ONE line of
`go.mod`. Do not add a second copy of any of them.
