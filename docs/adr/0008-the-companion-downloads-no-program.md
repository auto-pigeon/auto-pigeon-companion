# ADR-0008: The Companion downloads no program, and the extractor ships beside it

- Status: Accepted
- Date: 2026-09-23
- Affected components: AUCOM (and AUE, as the program a release carries)
- Decision owners: the operator, 2026-09-23 (HITL)
- Supersedes: [ADR-0004](0004-acquisition-is-verified-or-it-does-not-happen.md)

## Context

[ADR-0004](0004-acquisition-is-verified-or-it-does-not-happen.md) made the
Companion able to download programs — the map-building toolchains and the
Auto-Pigeon Extractor (AUE) — through a signed, revocable acquisition catalogue:
trust anchors, a keyring, a catalogue, a serial ratchet, sticky revocation, a
content-addressed tool cache, licence acknowledgement, and a signed
compatibility manifest naming which extractor a Companion could run. It was
built and tested, and it never ran in production: no production signing key was
ever made, and no signed catalogue was ever published. Every user who built a map
got their tools some other way.

The operator decided, on 2026-09-23:

> "AUE binary should be (will be) built in github CI of AUCOM, inserted in AUCOM
> and this will appear in the github releases ... So all the download mechanisms
> should disappear."

## Decision

**The Companion downloads no program.** `internal/catalog`, the root `catalog/`
directory, the managed downloads, cache, garbage collector and licence
acceptance in `internal/acquire`, the AUB-hosted extractor download grant, the
`/api/v1/profiles/{id}/acquire` routes, `companion catalog …`, and
`companion acquire plan|install|accept|list|verify|use|gc` are removed. The
removed subcommands print that they were removed and exit 2.

**A program the Companion runs is one the person installed.** `internal/acquire`
finds executables by three routes — `user_path`, `system_path`,
`already_installed` — and `companion acquire resolve` is its one command. A
profile may carry its project's homepage, which the Companion shows as a link;
it never fetches from it.

**The extractor ships beside the Companion in its release, as its own file.**
`build/bundle-sidecar.sh --extractor <prebuilt file>` copies a prebuilt AUE into
the bundle as `auto-pigeon-extractor[.exe]`, and `build/bundle-manifest.py`
lists it with its SHA-256, version, licence (`AGPL-3.0-only`) and corresponding
source. `internal/aue` finds it in exactly two ways:

1. **bundled** — the file beside the Companion's own executable. If a
   `bundle-manifest.json` beside it lists that file, the digest must match or
   it is refused; not listed, it runs unverified and says so; an unreadable
   manifest is a refusal. It must pass the protocol handshake against the
   compiled-in `aue.RequiredProtocol` (`1.0`: same major, minor at least).
2. **developer override** — `AUCOM_AUE_BINARY`, unverified, no handshake,
   labelled UNVERIFIED everywhere.

No fallback runs between them.

**Building AUE in this repository's release workflow is deferred** until the
repositories move from `andrea-dintino` to the `auto-pigeon` GitHub organisation.
AUE's repository is private, and reading it from AUCOM's CI needs a token that
would have to be replaced after the move. Until then a release bundle carries no
extractor, and its manifest says so (`"extractor": null`, `extractor_absent`).

## Consequences

### Positive

- The largest attack surface the Companion had — fetching and running somebody
  else's program — is gone rather than mitigated.
- A whole trust apparatus that had no production key, and therefore did nothing
  for any user, no longer has to be maintained, explained or threat-modelled.
- The extractor a user runs is the one the release shipped, and the release
  manifest is what says which bytes that is.

### Negative

- Users obtain toolchains themselves, from each project's homepage, and point a
  profile at the folder. The built-in `ericw-tools-q1`/`ericw-tools-q2`
  profiles no longer offer a download.
- A withdrawn toolchain build can no longer be revoked remotely; the user is the
  authority over what they installed, as they already were for `user_path`.
- Until the CI job exists, release archives carry no extractor, and map
  conversion needs a development override.

### Risks

- Shipping AGPL-3.0 software beside an MIT program is a redistribution. The
  bundle manifest names the licence and the corresponding source, but the
  bundle scripts do not yet place the licence text and copyright notice as a
  file — see `THIRD_PARTY_NOTICES.md`. The CI job that first ships an extractor
  must.
- A bundled extractor that no manifest lists runs unverified. That is recorded
  in the provenance a user sees, and it still has to pass the protocol check.

## Legacy data stays readable

- The `managed_download` acquisition mode and the `catalog_package` field are
  still accepted by the profile schemas, so published and older documents load.
  That route is never offered and never taken (`acquire.ErrNoDownloads`).
- The `installs` field of bindings and job records is read, never written.
  Build manifests no longer record it.
- `config.json` settings `catalog_url`, `catalog_anchors_path` and
  `tool_cache_dir`, and the environment variables `AUCOM_CATALOG_URL` and
  `AUCOM_CATALOG_ANCHORS`, are ignored; a config that carries them still loads.

## Threat model

`internal/threat` was rewritten with the code: T05 is now *something makes the
Companion fetch a program and run it*; T06 *the extractor beside the Companion is
not the one the release shipped*; T08 an extractor check that could not run; T09
the developer override mistaken for the shipped one; T44 antivirus against the
shipped extractor; T46 the copyleft source offer for it. T29 is removed, and the
catalogue parts of T35, T37, T39 and T43 are gone.

## Alternatives considered

- **Keep the catalogue for toolchains, drop it only for the extractor.**
  Rejected by the operator: all download mechanisms go.
- **Embed the extractor in the Companion's executable.** Already rejected — see
  `THIRD_PARTY_NOTICES.md`: it puts AGPL bytes inside an MIT artifact with
  nothing checking them.
- **Build AUE in AUCOM's CI now, with a personal token.** Deferred, not
  rejected: the token would have to be replaced when the repositories move.

## Evidence

- `internal/aue` — `TestABundledExtractorListedInTheManifestIsVerified`,
  `TestABundledExtractorWhoseDigestDisagreesIsRefused`,
  `TestNoBundledExtractorIsASentenceNamingTheOverride`.
- `internal/acquire/modes_test.go` — the three routes, and `ErrNoDownloads` for
  a legacy `managed_download` route.
- `internal/release` — `TestEveryShippedCopyleftComponentOffersItsSource`.
- `.github/workflows/build.yml` — assembles a bundle without and with a
  stand-in extractor and checks both manifests.

## Follow-up work

- After the move to the `auto-pigeon` organisation: a release job that checks
  out AUE at a pinned tag, builds it per platform, and passes it to
  `build/bundle-sidecar.sh --extractor`, together with its licence text.
