# ADR-0004: Acquisition is verified, or it does not happen

- Status: Accepted
- Date: 2026-09-07
- Affected components: AUCOM
- Decision owners: `AUCOM 206`

## Context

[ADR-0001](0001-profiles-are-data-and-the-executor-is-the-only-thing-that-runs.md)
settled that a profile is data. [ADR-0003](0003-one-executor-and-the-record-is-what-says-a-job-ran.md)
settled that one executor runs what a profile describes. Neither says how the
program a profile describes gets onto the machine, and that is the step where
this project puts somebody else's executable on somebody's disk and then runs
it. It is the highest-consequence thing the Companion does.

Three constraints were already fixed before this decision:

- The map-building tools are GPL-2.0 and this repository is MIT. They coexist
  because the tools are separate programs, downloaded independently and invoked
  as separate processes — see `THIRD_PARTY_NOTICES.md`. Nothing may be vendored,
  linked, or embedded.
- `AGENTS.md` forbids any component compiling in where another component lives.
  A download location is exactly such an address.
- A profile is a portable document that other people write. `internal/profile`
  refuses absolute paths, home directories and network locations inside one.

What was there instead was `internal/tools`: an empty registry, a fake tool, a
`Manager` with a `Resolve` and an `EnsureDownloaded`, and a digest check. It was
scaffolding, it said so, and nothing imported it. Its digest came from a Go map
compiled into the binary, which answers "were these the bytes we expected" and
not "who says these are the right bytes to expect".

Three questions had to be answered together, because answering any one of them
alone produces something that looks safe and is not.

**Where does the digest come from?** A digest in the profile means a profile
that has to be republished to withdraw a build, and a profile a user has already
granted keeps pointing at the old bytes. A digest compiled into the Companion
means a release to withdraw a build. Withdrawal has to be faster than either.

**What happens when verification cannot be performed?** A network failure, an
absent trust root, an expired document. Every one of them has an obvious
convenient answer — carry on, this once — and that answer is the whole
vulnerability.

**What happens after installation?** A cache lives in a directory the user's own
account can write to, and so does everything else running as that user. A
program that verifies at install and not at use has verified a historical fact.

## Decision

**A profile declares how, never where from.** `managed_download` names a
catalogue package. The URL, size, digest, signer, upstream source, licence and
corresponding-source offer live in the catalogue, which is versioned and
revocable independently of every profile that points at it.

**The catalogue is signed, through two levels.** Trust anchors — Ed25519 public
keys an operator installs as a file — sign a keyring; the keyring's catalogue
keys sign the catalogue. Two levels because the two documents have different
lifetimes: a catalogue changes when a tool is published, a keyring when a key
does, and signing every catalogue with the anchor would put the anchor's private
key online.

**No private key is in this repository and none is compiled into this build.**
The anchors are configuration, named by `AUCOM_CATALOG_ANCHORS`, with no default
and no fallback.

**Verification checks five things, in order, every time**: the payload is
exactly its own canonical re-encoding; a permitted, in-window, unrevoked key
signed it; it has not expired; its serial is not below the highest already
accepted; nothing it names is revoked.

**A failure to verify is a refusal.** There is no code path from any of "the
network is down", "no anchor is configured", "the signature did not verify",
"the catalogue expired" or "the serial went backwards" to a download that
happens anyway, and no flag that produces one.

**Revocation is sticky.** A revoked key or withdrawn artifact is recorded on
first sight and never forgotten; a later signed document cannot reverse it. The
rollback ratchet and the revocation list live beside `config.json`, not in the
cache.

**Downloads are bounded, verified, then extracted; archives are refused rather
than sanitized.** Size enforced while reading, digest checked before anything is
opened, and traversal, links, devices, setuid bits, duplicates and bombs refused
outright. Installation is one rename of a fully-formed directory into a
content-addressed cache.

**Verification continues at use.** The declared executables are re-hashed
against the install record every time an entry is used, and the sticky
revocation list is consulted then too.

**Offline changes availability, not checking.** An install record carries what
was verified and by whom, so using an installed package needs no catalogue.
Catalogue expiry bounds accepting *new* content; it does not expire a tool
already verified and installed.

**Cache cleanup removes only what nothing refers to**, with references taken
from bindings and from job records — a live dependency and retained evidence.

**`internal/tools` is retired.** Its ideas are here; its code was a second
implementation of the same job.

## Consequences

### Positive

- Withdrawing a compromised build is one signed catalogue with a higher serial,
  and it reaches machines that already installed it.
- The failure mode "verification was not configured, so it was skipped" is not
  representable: a build with no anchors installs nothing.
- Two pinned versions of a tool coexist, because entries are addressed by
  content and the collector removes only unreferenced ones.
- A user is shown the licence, the upstream project, the corresponding-source
  offer, the size, the digest and the signer before the first download.
- The whole path is testable with an in-process TLS fixture and no public
  network, including rotation, expiry, rollback and revocation.

### Negative

- Nothing can be downloaded until an operator configures anchors and an address.
  That is a real cost paid deliberately: the alternative is a default that is
  either useless or a trust root this project cannot vouch for.
- The signed payload is base64, so a catalogue is not readable in a text editor.
  `companion catalog show --json` prints it.
- Publishing is more work: two documents, two keys, a serial to raise.
- Re-hashing executables on every use costs I/O on a large toolchain.

### Risks

- **The anchor key's private half is a single point of failure.** Rotating it
  means a new release, because anchors are configuration on each machine.
  Mitigated by the anchor being offline and by the keyring absorbing ordinary
  rotation, not by anything in the code.
- **Sticky revocation cannot be undone by mistake.** A revocation published in
  error is permanent on every machine that saw it, short of a user deleting
  `catalog-state.json`. That is the intended asymmetry, and it is a real
  operational hazard.
- **Expiry is not enforced for use.** Somebody who can keep a machine offline
  can keep it using a tool indefinitely. Revocation still reaches it if the
  revocation was seen before the machine went offline; if it never was, it does
  not. Recorded rather than solved.
- **The compression-ratio and entry-count ceilings are guesses.** They are
  generous enough for any real toolchain today and will need revisiting.

## Alternatives considered

- **Digest in the profile.** Rejected: makes withdrawal a republication of every
  document that points at a build, and leaves an already-granted profile
  pointing at the old bytes.
- **Digest compiled into the Companion.** Rejected: makes withdrawal a release,
  and answers the wrong question — what bytes were expected, not who says so.
- **One signing level.** Rejected: it puts the trust root's private key online,
  which is what makes it not a root.
- **Trusting TLS alone.** Rejected: it authenticates a host, not a build, and
  says nothing after the bytes are on disk. TLS is still required for the
  transport, for confidentiality and to make downgrade a visible act.
- **Sanitizing hostile archive members instead of refusing them.** Rejected: an
  archive that names `../../../.ssh/authorized_keys` meant it, and writing it
  somewhere else is still acting on a document that has proved hostile.
- **Automatically re-downloading over a tampered cache entry.** Rejected: it
  erases the only evidence of what changed, and turns an incident into a loop.
- **A `--insecure` escape hatch.** Rejected outright. It is the flag every
  hostile instruction tells a user to pass.
- **Keeping `internal/tools` alongside the new packages.** Rejected: two
  acquisition implementations means every guarantee has to be made twice — the
  same argument ADR-0003 made about executors.

## Evidence

- `internal/catalog` — envelope, keyring, catalogue, trust state, verifier.
- `internal/acquire` — the four modes, downloader, extractor, cache, collector.
- `internal/catalog/catalog_test.go` — signing, key-id derivation, role
  separation, edited payloads, non-canonical payloads, duplicated members,
  expiry, validity windows, rotation, revocation, rollback, misattributed
  entries, absent anchors.
- `internal/acquire/acquire_test.go` — valid install, cache hit, concurrent
  install, interrupted download and retry, wrong digest, wrong size, over-long
  response, absent platform, expired catalogue, rollback, revocation on install
  and on use, eleven hostile archives, offline, tamper on use, added file,
  licence acknowledgement, four refusals that never fall back, garbage
  collection, redaction.
- `internal/acquire/modes_test.go` — the four routes, and the import direction.
- `internal/cli/acquire_cmd_test.go` — keygen, sign, verify, status, and an
  install driven end to end through the CLI against an in-process TLS server.

## Follow-up work

- No real tool is published in any catalogue yet, and no production signing key
  exists. `THIRD_PARTY_NOTICES.md` carries the open question of which GPL-2.0
  tools and versions.
- The GUI has no acquisition screen; everything here is CLI-only so far.
- The keyring has no signature threshold: one anchor signature is enough. A
  threshold is the obvious next step if more than one person holds an anchor.
