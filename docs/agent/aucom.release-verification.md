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

**The kit ships INSIDE every artifact.** `acceptance/run-acceptance.sh` and
`acceptance/run-acceptance.ps1`, plus `kit-options.json`, go into all six
archives. A platform whose artifact nobody can run stays `build_only` until
somebody with that machine runs it, and the only way that is a single
instruction rather than a checkout and a toolchain is if the kit is in the
download. Both entry points ship in every artifact, deliberately: a Linux
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
