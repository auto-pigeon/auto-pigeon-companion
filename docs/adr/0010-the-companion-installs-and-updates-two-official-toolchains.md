# ADR-0010: The Companion installs and updates the two toolchains Auto-Pigeon maintains, and nothing else

- Status: Accepted
- Date: 2026-10-28
- Affected components: AUCOM
- Decision owners: the operator, 2026-10-28 (HITL), in `NEW_367_AUCOM_Managed-Compiler-Install-And-Hourly-Updates`
- Supersedes in part: [ADR-0008](0008-the-companion-downloads-no-program.md) — only its "downloads no program" rule, and only for these two toolchains

## Context

[ADR-0008](0008-the-companion-downloads-no-program.md) removed every download
path: a program the Companion runs is one the person installed. Since then
Auto-Pigeon has come to maintain its own builds of the two map compilers every
built-in build profile needs — `auto-pigeon/ericw-tools` (qbsp, vis and light
for Quake 1 and Quake II) and `auto-pigeon/q3map2` — published as GitHub
releases with a release manifest, per-platform archives, a file list inside
each archive, and the fork's own acceptance record per target. A first launch
still began with a manual hunt: download, check `SHA256SUMS`, unpack, choose the
folder in Profiles.

The operator decided, on 2026-10-28, that the Companion installs those two
toolchains automatically on first launch and, while it runs, checks their
releases about hourly and **offers** updates — never applying one unasked. The
same decision reserves the Companion's self-update for `NEW_368`.

## Decision

**Exactly two toolchains, compiled in.** `internal/managedtools` is the one
owner. Its allowlist is two repositories, two tag families
(`auto-pigeon-ericw-v<version>`, `auto-pigeon-q3map2-v<version>`), the release
and archive manifest schemas this build reads, and the GitHub hosts a request
or a redirect may reach (`api.github.com`, `github.com`,
`objects.githubusercontent.com`, `release-assets.githubusercontent.com`,
`github-releases.githubusercontent.com`), over HTTPS. No profile, binding,
config file, environment variable or page can add a source. A release build has
no override; a loopback-only seam exists for tests and for a build made with
the `aucom_fixture_releases` tag.

**Generic acquisition is unchanged.** `internal/acquire` still only finds
programs and imports no HTTP client; a document's `managed_download` route is
still read and refused. The binding mode the managed installation uses,
`managed_official`, is not in the document schema, so no document can declare
it. Engines, game data, assets and any other tool are never downloaded.

**What makes a release eligible.** Not a draft; a prerelease only on the beta
channel (the shipped default, because every official release is a prerelease
today); a version in the tag family, ordered numerically (1.10 > 1.9,
ap.3030 > ap.3026), never older than the release this Companion was built
against; a release manifest of a known schema whose tag, version and
prerelease flag agree with GitHub; a published build for this OS and processor
with a passing acceptance, a SHA-256 and a size that agree with GitHub's listing
and with GitHub's own digest when it lists one; a version banner the strict
built-in profile accepts and does not refuse by name. The newest eligible
release is the candidate; why each newer one is not is shown.

**Verification before anything runs.** The archive is downloaded into a staging
folder with size and disk bounds, hashed against the manifest and GitHub's
digest, unpacked under bounds on entries and bytes with every escaping,
absolute, linked, device-named or case-colliding entry refused, every file
checked against the archive's own file list, each program's header checked
against this processor, and then made an installation by one rename. Only then
does the executor's own version check ask the programs what they are, under the
existing bounds and approval rule; a mismatch removes the installation.

**What it is bound to.** Every built-in profile of the tool that nobody set up
— the upstream-named defaults the built-in pipelines use, and the Auto-Pigeon
build alternatives — in one locked write of `bindings.json`, so Quake 1 and
Quake II never name two EricW versions. A program a person chose is never
replaced; Settings offers the managed installation beside it.

**Updates are offered, and switched only when nothing uses the old version.**
Update now, Update when I close Companion, or Cancel with an optional "don't
ask again for this version" that suppresses exactly that version's bytes for
that tool. Each version is its own folder; a job pins absolute paths; every
pipeline run holds a gate the activation needs exclusively; the previous
version is kept for rollback, and retention removes only what nothing uses.

**The trust statement is honest.** A digest in the same release proves the
download is the one the release describes, not who published it. The page and
the install record say "not signed". This residual is accepted, with an owner
and a review date, as threat row T55.

## Consequences

### Positive

- A first launch is ready to build without a manual download, on every
  platform the releases qualify.
- Every built-in build profile runs the toolchain Auto-Pigeon measured, and a
  build records exactly which release and bytes ran (`tools[].managed`).
- A newer compiler reaches users without a Companion release, and never under a
  running build.

### Negative

- The Companion again contains code that downloads and unpacks programs. It is
  small, closed and tested, but it is attack surface ADR-0008 had removed.
- A user who wants no downloads has to turn it off (`managed_tools_off`,
  `AUCOM_MANAGED_TOOLS=off`); `companion serve` started by a script does
  nothing unless asked (`--managed-tools on`).

### Risks

- Repository compromise: see T55's residual.
- GitHub's anonymous rate limit (60 requests an hour per address). One check an
  hour per tool is two to four requests; a 429 or an exhausted limit is honoured
  and shown, never retried in a loop.

## Alternatives considered

- **Restore the signed catalogue of ADR-0004.** Rejected: no production key
  ever existed, and the operator chose the official releases as the trust root.
- **Download from `/releases/latest`.** Rejected: it excludes prereleases, which
  every official release is today.
- **Apply updates silently.** Rejected by the operator: updates are offered.
- **Build the compilers from source on unsupported platforms.** Rejected: an
  unsupported platform says which tool has no qualified build and keeps manual
  setup.

## Evidence

- `internal/managedtools` — the allowlist, the production source's host and
  scheme policy, every eligibility reason, every archive refusal, the schedule
  across restarts, ETag/304 and 429, the offer decisions and their scoping,
  activation waiting for work, rollback, retention, close and crash.
- `internal/web/new367_test.go` — verification through the executor, one EricW
  installation for Quake 1 and Quake II, a person's own binding left alone, an
  update waiting for a running pipeline, and a real build recording the bytes.
- `internal/threat` — T05 (rewritten), T55, T56.

## Follow-up work

- `NEW_368`: the Companion's own update, which consumes this coordinator and
  verification, and the native Windows/macOS rows of this one.
