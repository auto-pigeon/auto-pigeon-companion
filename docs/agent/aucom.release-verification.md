---
id: aucom.release-verification
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: A build is not a verification, and five states keep them apart
authority:
  - aucom-native-support-states
  - aucom-release-artifacts
topics:
  - release
  - build
  - artifacts
  - native
  - platform
  - windows
  - macos
  - matrix
  - verification
  - bundle
  - acceptance
  - push
  - authorization
paths:
  - internal/release/**
  - internal/nativeacceptance/**
  - build/**
---

# A build is not a verification, and five states keep them apart

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 549-625 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

## 8. A BUILD IS NOT A VERIFICATION, AND FIVE STATES KEEP THEM APART (`AUT/AUCOM 231`)

`build/release.sh` produces six artifacts. That is six statements that this code
COMPILES for six targets, and it is not one statement about whether the program
starts, registers a URI scheme, finds a compiler, writes a PAK or composes an
engine command on any of them. `219` asked one Linux-amd64 agent to execute
Windows, Linux-arm64 and macOS targets and to launch Quake without owned game
data; none of that is something an agent on one machine can do, and none of the
failures would have been a product defect. `231` made the evidence model honest
and shipped the kit that closes a row.

**`internal/release/native-support.json` is the declaration, and
`StateOf` is the one derivation.** Five states, and `build_only` and
`manual_pending` are the ABSENCE of evidence rather than weaker passes:

```text
native_pass     a bundle from that platform passed
native_fail     a bundle from that platform failed
manual_pending  a native host exists and nobody has run it yet
build_only      an artifact is produced and no native host is declared
unsupported     no artifact is produced
```

There is no branch from either of the middle two to `native_pass` that does not
go through a verification record, and a verification record names the bundle and
the digest it came from. `auto-pigeon-tools/scripts/aucom/matrix.py` implements
the same rule in the same words over imported bundles, and reports a row this
file claims that no bundle supports. Do not add a flag that accepts a build as
evidence, and do not widen `built` into `supported` in a release note.

**The kit is NOT in the download; it runs from a checkout.** Until
2026-09-25 `acceptance/run-acceptance.sh`, `acceptance/run-acceptance.ps1` and
`kit-options.json` went into every archive. The operator decided an archive
carries what runs the Companion and nothing else — no acceptance kit, no
licence or notice files — so the owner runs the kit from a checkout against an
unpacked archive: `acceptance/run-acceptance.sh --companion
<unpacked archive>/companion` (`.ps1` with `-Companion`). The scripts check the
`SHA256SUMS` beside the program they are given, which is still in every
non-macOS archive. A platform nobody has run stays `build_only` until somebody
with that machine does. Both entry points stay in `acceptance/`: a Linux
machine with `pwsh` and a Windows machine with a POSIX shell are both real.

**The LANES are in the program, once.** `internal/nativeacceptance` drives THIS
BINARY as a child process, lane by lane. Two hand-written harnesses in two shell
languages would be two contracts that agree until the day they do not, and only
one of them would ever be executed by the person maintaining them. What is in
the shell is the half a program cannot do for itself: verify the artifact's
checksums BEFORE starting it, and read what the operating system calls the
machine — which is what separates a native arm64 run from an amd64 artifact
under Rosetta or Prism. `kit-options.json` is the one option table both scripts
are checked against.

**The bundle is CONSTRUCTED, not collected.** `internal/feedback`'s rule, for
the same reason: a filter is only as good as the last person who thought about
it, and a struct with no field for a path cannot leak one however carelessly a
future lane is written. `Bundle.Validate` then refuses a document in which
anything that still looks like an absolute path survived, and AUT re-checks the
result against its own allow-list in another language before merging it. Three
layers, and none of them is the mechanism alone.

**The owned-game lane records SIX facts and has nowhere to put a seventh.**
Family, profile and version, platform, the command SHAPE with paths replaced,
the signal, the elapsed time. No lane copies, archives, hashes wholesale or
uploads a byte of game data, and there is no flag that makes one. Preview and
launch are separate rows because they are separate claims: a preview that
printed a command is not evidence that an engine started.

**The run is isolated, and the purge lane is why.** Every lane runs against a
HOME the run made. `uninstall --purge` deletes configuration, granted profiles
and build history — an acceptance run that did that to the operator's own
machine would cost them the decisions they had made. The two things no environment variable moves are reported as
`not_applicable` with the reason rather than performed: a Windows HKCU
registration, which the installer owns, and a macOS bundle, which a loose binary
does not have.

**Quake II and Quake III stay visibly work in progress.** `companion release
support` prints the game families beside the platforms and calls both of them
experimental, in `internal/maturity`'s own sentences. A platform row and a
family row are different axes and neither is allowed to stand in for the other.

## The per-push bundled release (`NEW_247A`)

`.github/workflows/release.yml` publishes a prerelease `v1.<count>` on every
push to main, and **every decision it makes is `build/release-plan.py`**: the
library revision, the target join, the bundle layout, the archive checks, the
release manifest, the upload list, and whether a rerun may touch an existing
release. The YAML holds no target list, no bundle format and no version
arithmetic. `cmd/companion/release_contract_test.go` fails if it grows one, and
fails for each fault the prompt listed.

**The converter is this commit plus one exact library revision (`NEW_371`).** `build/release.sh`
builds `cmd/mapio` for every target against the `go/mapio` revision `go.mod` pins, and that line is the
only place the revision is written. `check-converter` then reads, from what the toolchain recorded in
each built file, that it links that module at that version and nothing else, was built without cgo, and
is for the platform it is filed under, and it runs the host's converter to ask what it is. No other
repository is checked out and no job reads a secret, so a release builds from a clone. The bundle
manifest, the release manifest and the release notes carry the library's version and commit.

**The APMap a release can read is that revision's, so a new contract needs a `go get` here.** The old
rule, that each release took the head of the extractor's branch, is superseded: it protected a release
from falling behind the APMap version the editor saves, and that protection is now a deliberate step.
When AULIBS promotes an APMap version, move this repository's pin in the same change that needs it, and
never by resolving `main` in a workflow: two jobs resolving a branch independently can compile one binary
against a revision another's manifest does not name.

**Which targets release is `build/converter.json`'s own statement**, joined with
`internal/release/native-support.json`. The converter inherits no other program's support matrix.
`windows/arm64` stays `build_only` until a native job has run it. A target that compiles is not a target
that has been executed, and the release manifest keeps those two facts apart.

**A Companion that bundled the extractor does not update itself to this shape, and a release is never
changed to make it.** The manifest schemas are `aucom.release-manifest/2.0` and `aucom.bundle-manifest/2.0`;
an older Companion refuses them, and no change here reaches a program already installed. What can carry one
across is a **migration bridge**, which is not a release: `aucom.managed-tools` rule 13 and ADR-0011 own it.
Until one is reviewed and published, its user follows the manual steps in `README.md`, and the release notes
say so. Making an ordinary release readable by an old updater would need a manifest that calls the converter
the extractor, or an extractor in every bundle. Old release manifests and assets are never rewritten.

**Acceptance converts every fixture, not one.** `build/release-fixtures/` holds
the APMap 1.3 slab kept as backward-compatibility evidence and a Quake 1 APMap
1.5 document with authorship and three nested groups; `expectations.json` says
what each `.map` must hold, a fixture without an entry fails the run, and a
document declaring a version nobody defined must be refused.

**The join verdict and the native-support state are two axes.**
`bundled_release`, `build_only` and `refused` answer one question: may this
archive be a download? That depends on both repositories' release
authorities. `StateOf` answers a different one: has this program been run on
this hardware? A `bundled_release` darwin/amd64 whose Companion state is
`build_only` is honest, and the release notes print both. Do not merge them.

**CI native acceptance is evidence for one run, not a verification record.**
`build/release-acceptance.py` runs the exact archive on one hosted runner per
OS family (`NATIVE_RUNNERS`), and refuses to run on a machine the archive is
not for. A pass there is not written into `native-support.json`. Only a
returned kit bundle is.

**A raw socket is not a page (NEW_254).** Step 8 holds a lease with a
hand-written WebSocket; step 9 (`browser close`) opens the Companion in a real
Chromium-family browser found on the runner — two tabs, close one, close the
last in a browser that stays open — through the browser's DevTools HTTP
endpoints and `loopback_http`, a raw socket to 127.0.0.1, because the release
tools may hold no HTTP client (`TestNoWorkflowOrReleaseToolRegainsADownloadPath`).
A machine with no such browser reports `skip`, which is not a pass. Both steps
start `serve --interactive`, the no-argument launch minus the system opener,
and both are headless evidence: neither is a person's double-click, and
neither moves a row of `native-support.json`.

**Browser cold startup is measured separately from closing an AUCOM page.**
Release run `37821483904` passed every build and the raw-socket lifecycle gate,
then timed out waiting for Chrome's DevTools profile file. The harness now
allows 120 seconds for browser startup, retains a bounded stderr tail on
failure, and records the process state before cleanup so its own termination
signal is not presented as Chrome's exit. The harness-only application startup
window allows that cold start plus the first page's existing wait; production
startup and close-grace defaults are unchanged. A present browser that cannot
start still fails acceptance. `build/test_release_acceptance.py`, driven by
`go test`, checks delayed startup, real process exit, transient file access and
cleanup; the native gate still validates two actual browser tabs and AUCOM's
leases.

**Reruns compare and never overwrite.** Archives are written by
`releaselib.write_zip` with fixed times, order and permissions, so one commit
gives one set of bytes. `reconcile` fails on any attached asset whose digest
differs, is missing, or was not produced by the build.



## Pushing to main is pre-authorized here (HITL, 2026-10-21)

**This module is the one authoritative home of this rule;** `AGENTS.md` §2 and AUT's shared no-push policy
(`auto-pigeon-tools/AGENTS.md` §2, `docs/agent/aut.branch-policy.md`, the branch rule `run-agent.sh` dispatches) only
point here.

Andrea (HITL) authorized, on 2026-10-21, as a **standing exception for this repository only**: an agent may commit
to and push `main` of `auto-pigeon/auto-pigeon-companion` whenever its assigned work needs it — now and in later
tasks — without asking again. That includes the prerelease the push publishes through `release.yml` (above) and
the follow-up pushes needed to repair a CI or release failure the task caused. Every other repository keeps its own
policy: there, no push happens unless the prompt asks for one.

What the authorization does not cover, and what every push still owes:

- **Never** force-push, rewrite published history, or move, delete or replace a tag or a release asset. Preserve
  other people's commits: fetch first, and stop on anything that is not a fast-forward.
- **Batch.** Every push to `main` is a release (`v1.<count>`), so push one coherent group of task commits, not
  prose-only checkpoints one at a time.
- **Check before** — `go build ./...`, `go vet ./...`, `go test ./...` and whatever the change's owner needs.
- **Follow after** — the exact commit's `build` and `release` runs to terminal results, every job (macOS and
  Windows included). Repair a failure the task caused on its owning code or test, then follow the next head. Do not
  weaken the CI policy or drop a platform's tests to get green.
- **Verify the publication** — the tag, `SHA256SUMS`, `release-manifest.json` (commit and bundled converter) and the
  extracted bytes of at least the archive for the host you are on.
- **Record** the commit, both run ids and the tag in the task's handoff.
- **CI is not acceptance.** A green release replaces none of the browser, released-binary or engine observations a
  prompt asks for; a hosted runner's pass is evidence for that run only (above).

The first push under it: `56684ce3` (`NEW_329D`), build run `39623990464`, release run `39623990547`, tag `v1.286`.

## Contributor commands (moved from README.md, 2026-10-17)

`README.md` is user-facing only; these are for whoever changes or releases this repository.

```console
$ gofmt -l .                 # all three must pass before a change is merged
$ go vet ./...
$ go test ./...
$ ./build/release.sh --out dist/local --targets linux/amd64,windows/amd64
$ build/release-acceptance.py --archive dist/auto-pigeon-companion-1.163-linux-amd64.zip \
    --platform linux-amd64 --version 1.163 \
    --fixture build/release-fixtures/release-acceptance.apmap
$ acceptance/run-acceptance.sh --companion ~/auto-pigeon-companion-1.158-linux-arm64/companion
```

`build/release.sh` builds all six targets, the macOS `.app`, an SBOM and `SHA256SUMS` into `dist/`,
and puts the Auto-Pigeon icon on the Windows executables and the `.app`, generated by `build/icon`
from the page's 128×128 mark in `internal/web/assets/brand/` (a Linux executable has no icon).
`build/release-acceptance.py` runs the same native acceptance CI runs against one archive, including
the real-browser close step (`AUCOM_TEST_BROWSER` names a browser it does not find by itself).
`acceptance/run-acceptance.sh` (`acceptance\run-acceptance.ps1 -Companion …\companion.exe` on
Windows) is the native kit for hardware CI does not have: it runs from a checkout against an
unpacked archive and writes a result bundle to send back.
