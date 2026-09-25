# Third-party notices

`auto-pigeon-companion` is MIT licensed, Copyright (c) 2026 Andrea D'Intino.
That licence covers **this repository's own code only** — see [LICENSE](LICENSE).

This document explains the licensing status of everything else the application
touches: what it depends on, what it runs, and what it redistributes. Those are
three different relationships with three different obligations, and they are
kept apart here on purpose.

**A release archive is not under one licence.** One that carries the extractor
holds three kinds of thing, and each keeps its own terms:

- **Auto-Pigeon Companion's own code — MIT** (`LICENSE` in this repository).
- **Contract files from `auto-pigeon-libraries` (AULIBS) compiled into the
  Companion — Apache-2.0**, unmodified. See below.
- **Auto-Pigeon Extractor (AUE), shipped beside the Companion — proprietary**
  (`LicenseRef-Auto-Pigeon-Proprietary`, the Auto-Pigeon Proprietary Software
  License, Copyright (c) 2026 Andrea D'Intino, all rights reserved), under its
  own licence, whose text is `LICENSE` in the extractor's repository. The MIT
  licence does not cover it. See below.

**No licence or notice file ships inside a release archive** (operator
decision, 2026-09-25): an archive carries the Companion, the extractor, their
`bundle-manifest.json` and a `SHA256SUMS`, and nothing else. The licence texts
and this file are in the repositories; each archive's `bundle-manifest.json`
names the licence of every file in it, and the release notes say where the
texts are.

Third-party programs the Companion runs — compilers, engines, games — are
installed by the user and stay under their own licences.

## Summary

| Component | Licence | Relationship | Redistributed by us? |
| --- | --- | --- | --- |
| This repository's code | MIT, Copyright (c) 2026 Andrea D'Intino | is the product | yes |
| Go standard library | BSD-3-Clause | compiled in | yes, as part of the binary |
| Go module dependencies | — | none exist | — |
| `@auto-pigeon/operational-notice-contract` 1.0.0 (`src/index.mjs`, two schema files), from auto-pigeon-libraries | Apache-2.0 | vendored byte for byte into the embedded page (`internal/web/assets/vendor/operational-notice-contract/`) | yes, inside the binary |
| `@auto-pigeon/incident-contract` data (`incident-codes.json`, `redaction-rules.json`), from auto-pigeon-libraries | Apache-2.0 | embedded byte for byte (`internal/incident/contract/`) | yes, inside the binary |
| `@auto-pigeon/incident-contract` 1.4.0 (`src/*.mjs`, `schema/*.json`), from auto-pigeon-libraries | Apache-2.0 | vendored byte for byte into the embedded page (`internal/web/assets/vendor/incident-contract/`) | yes, inside the binary |
| auto-pigeon-extractor (AUE) | **LicenseRef-Auto-Pigeon-Proprietary** (Auto-Pigeon Proprietary Software License; all rights reserved) | separate process, shipped as its own file (`auto-pigeon-extractor[.exe]`) beside the Companion in a release bundle | named in the bundle manifest; the text is in the extractor's repository |
| ericw-tools 0.18.1 (qbsp, vis, light, bspinfo, bsputil) | **GPL-3.0-or-later** as distributed (GPL-2.0-or-later source) | separate process, **obtained by the user** from its homepage; nothing is downloaded | no |
| ericw-tools 2.0.0-alpha7 (the Quake II line) | **GPL-3.0-or-later** as distributed (GPL-2.0-or-later source) | separate process, **obtained by the user** from its homepage; nothing is downloaded | no |
| Q3Map2 2.5.17n (NetRadiant-custom `20260114`) | **GPL-2.0-or-later** | separate process, **found by the user**; nothing is downloaded | no |
| ioquake3, and any id Tech 3 engine | **GPL-2.0-or-later** | separate process, already installed | no |
| Games the user launches | the user's own | separate process, already installed | no |

## Go dependencies

None. The module has no third-party Go dependencies: `go.mod` lists only the Go
version, and there is no `go.sum` because there is nothing to check sums for.
Everything is the standard library, which is BSD-3-Clause and whose notice
travels with every Go binary.

If a dependency is ever added, its licence goes in this document, and it must be
compatible with redistributing the Companion under MIT.

**That claim is checked, and against the binary rather than against `go.mod`.**
`go.mod` states an intent; the binary states a fact, and the fact is what people
run. `internal/release` reads the module graph with `debug.ReadBuildInfo`, so a
build with a `replace` directive, a vendored tree or a toolchain-injected module
would appear there and nowhere else — and
`release.TestThisProgramLinksNoExternalModule` fails when it is not empty. The
same graph is what a user sees:

```console
$ companion security audit
Go module dependencies: none.
```

A release publishes it as a CycloneDX SBOM
(`companion release sbom`), listing this module, its empty module graph, and
every external program the Companion runs or ships beside itself — each with
the relationship it has to the artifact, because a component shipped beside the
binary, or installed separately and run by it, is not a component nobody should
be told about.

## Auto-Pigeon contract files from auto-pigeon-libraries — Apache-2.0

Files from the Auto-Pigeon project's own shared library,
`auto-pigeon-libraries` (Apache License, Version 2.0), are compiled into the
binary unmodified: the operational-notice contract's `src/index.mjs`,
`schema/notice-rules.json` and `schema/operational-notices-response-1.0.schema.json`,
which the page runs to decide which notices to show; the incident contract's
`schema/incident-codes.json` and `schema/redaction-rules.json`, which
`internal/incident` reads; and the incident contract's `src/` and `schema/` files
the page uses to build a bug report (`internal/web/assets/vendor/incident-contract/`).
They are not Go modules and are not in the module graph.
Tests compare each copy with its source (`TestVendoredNoticeContractIsExactlyAULIBS`,
`TestEmbeddedContractIsExactlyAULIBS`, `TestVendoredIncidentContractIsExactlyAULIBS`),
so a copy cannot be edited here.

**They stay Apache-2.0 inside the Companion's binary.** The Companion's MIT
licence does not relicense them; `companion security audit` and
`companion release sbom` list them as Apache-2.0 components in the artifact, and
a bundle manifest's `licenses` list names them separately from the Companion.
Apache-2.0 is compatible with redistributing them inside an MIT program; its
text is at https://www.apache.org/licenses/LICENSE-2.0. AULIBS ships no `NOTICE`
file, so there is no NOTICE text to reproduce.

## auto-pigeon-extractor (AUE) — proprietary, shipped beside the Companion and never inside it

AUE is a **separate, proprietary program**: `LicenseRef-Auto-Pigeon-Proprietary`,
the **Auto-Pigeon Proprietary Software License**, Copyright (c) 2026 Andrea
D'Intino, all rights reserved. It is not part of this codebase, this
repository's MIT licence does not apply to it, does not relicense it, and
cannot. The Auto-Pigeon project's policy since `NEW_247G` (2026-09-24) is that
AUE is proprietary and private; before that it was published under
AGPL-3.0-only (see "Earlier AGPL-3.0 builds" below).

**Using the bundled extractor requires the copyright owner's authorization.**
Its licence grants no licence or other right to use it except through a
separate written authorization or agreement issued by the copyright owner.
Nothing in this repository, in the Companion's MIT licence, or in the fact that
a release archive carries the extractor grants that authorization. For
licensing inquiries or written authorization, contact the copyright owner.

**A release carries it as its own file, beside the Companion — never inside the
Companion's executable, and never downloaded.** The operator settled that on
2026-09-23: *"AUE binary should be (will be) built in github CI of AUCOM,
inserted in AUCOM and this will appear in the github releases ... So all the
download mechanisms should disappear."* See
[ADR-0008](docs/adr/0008-the-companion-downloads-no-program.md).

A bundle assembled without one has `"extractor": null` and an
`extractor_absent` entry in its `bundle-manifest.json`.

### The identifier is quoted from the extractor, never restated

The extractor declares its licence ITSELF — run `auto-pigeon-extractor protocol`
— and so does its release manifest (`license.spdx`). A release quotes that
answer: `build/release-plan.py` reads it from the pinned build's release
manifest, and the bundle manifest, the release manifest and the release notes
carry the same identifier. That is deliberate: a program under a different
licence must not be the thing that says what this one is licensed as. It ships
a copy of the claim, the copy goes stale, and the stale copy is the one a user
reads. So **the identifier the pinned build declares is the authority for the
extractor build in that archive**, and its text is the pinned commit's
`LICENSE` in the extractor's repository.

The one identifier a release refuses for the extractor is MIT — it never was,
and an archive listing it so would read as entirely MIT.

### How it is used

A release bundle places a **prebuilt** extractor beside the Companion as
`auto-pigeon-extractor` (`auto-pigeon-extractor.exe` on Windows), and
`bundle-manifest.json` lists it with its SHA-256, its version, its licence and
the commit it was built from — see [`build/bundle-sidecar.sh`](build/bundle-sidecar.sh)
and [`build/bundle-manifest.py`](build/bundle-manifest.py). The Companion finds
it in `dependencies/` beside its own executable (in `Contents/MacOS/` inside a
macOS `.app`), checks the digest when the manifest
lists it, asks it `protocol --json`, and then invokes it as a **separate
operating-system process** through `os/exec` and reads its stdout. See
[`internal/aue`](internal/aue/doc.go).

Communication is process-level only: subcommand, arguments, environment, working
directory, standard output, exit status. AUE's own packages all live under
`internal/`, so Go's internal-package rule makes importing them impossible even
by accident, and AUE never appears in `go.mod`.

### What that means for distribution, precisely

**Shipping it beside the Companion is a redistribution of AUE**, which its
owner, Andrea D'Intino, makes. It is **not** a combined work — two programs in
one archive, one running the other as a separate process, share no address
space and link nothing — so the Companion stays MIT and AUE stays proprietary.

Three things are enforced here rather than assumed:

1. **A bundle cannot carry it without its licence identifier and provenance.**
   `build/bundle-manifest.py` refuses `--extractor` without
   `--extractor-version`, `--extractor-source` and the full
   `--extractor-commit`, writes the declared identifier into the manifest's
   `extractor` entry and `licenses` list, and refuses an extractor declared
   MIT.
2. **The release's component list names it.** `companion release sbom` and
   `companion security audit` list `auto-pigeon-extractor` with
   `distribution: shipped-beside-in-the-release` and
   `LicenseRef-Auto-Pigeon-Proprietary`, with no corresponding-source link —
   that is a copyleft obligation, and the extractor is not copyleft.
   `release.TestTheExtractorIsListedAsProprietaryNeverMITOrCopyleft` fails if it
   is listed as MIT or copyleft, and
   `release.TestEveryShippedCopyleftComponentOffersItsSource` still fails any
   copyleft component shipped with no source offer.
3. **This file names it.** `TestNoticesCoverEveryRedistributedComponent` fails
   if this document stops naming `auto-pigeon-extractor`, its licence, or the
   authorization its use requires.

### Earlier AGPL-3.0 builds

Extractor builds published before `NEW_247G` declared `AGPL-3.0-only`, and
**copies distributed under that licence keep the rights it granted** — the
change of licence does not reach back. The Companion still accepts such a
build: its handshake records whatever licence a build reports, and a copyleft
build carries its corresponding-source link. A release archive whose pinned
extractor build declares `AGPL-3.0-only` carries that build's AGPL licence file
and says so in its release notes.

### What was wrong with embedding it, since the code for that existed

`internal/aue/embed.go` used to copy the platform-matching AUE binary into the
Companion executable with `//go:embed`, and a release built that way would have
put a separately licensed program (AGPL-3.0-only at the time) inside the
Companion's binary. It was never
released — `internal/aue/embedded/` only ever contained `.gitkeep` — and the
mechanism is gone, along with the build steps that staged it. CI fails if it
comes back: see the `no-embedded-extractor` job.

Embedding would not have made a combined work either — the bytes were written
to a temporary file and run as their own process. What was wrong with it is that
the extractor's bytes sat inside another program's artifact, under that
program's licence, with nothing checking them and nothing carrying the
extractor's own licence (then AGPL-3.0, with its notice and source offer). A
separate file listed in a manifest, with its own licence file, fixes all three.

### It is not downloaded any more, either

Between those two arrangements the Companion fetched the extractor at run time
against a signed, revocable catalogue, at the version a signed compatibility
manifest named. That mechanism is removed with every other download mechanism
(`internal/catalog`, `companion extractor install`, `companion catalog …`).
There is no route by which the Companion puts an extractor on a machine.

### The developer override redistributes nothing

`AUCOM_AUE_BINARY` points at a build the developer already has. It is local, it
is labelled UNVERIFIED everywhere it appears, and nothing produced with it is
uploaded or published.

## External map-building tools (GPL)

The external tools that build Quake maps are licensed under the **GNU General
Public License**. They are **not part of this codebase**, and this repository's
MIT licence does not apply to them.

Which version of the GPL is a question with a measured answer rather than an
assumed one, and the two halves differ — see the per-tool section below.

### How they are used

They are **obtained by the user** — from each project's homepage, a package
manager, or a copy that came with a game — as **independent, prebuilt
binaries**, and invoked as **separate operating-system processes** via
`os/exec`. The Companion downloads none of them. Communication is process-level
only: command-line arguments, environment, working directory, standard input
and output, exit status, and files on disk.

They are never:

- compiled into this binary,
- statically or dynamically linked against this binary,
- vendored into this repository,
- embedded with `//go:embed`,
- shipped in a release archive,
- or reached through any in-process calling convention.

No Go dependency may pull a GPL tool's source or object code into this
module. This is an architectural constraint, split across two packages that
each restate it in their own documentation:
[`internal/acquire`](internal/acquire/acquire.go) finds a tool's executables in
a folder the user chose, on PATH, or under a configured root, and downloads
nothing; [`internal/job`](internal/job/exec.go) runs the result — as an
executable path and an argument array handed to `os/exec`, never through a shell
and never through an in-process call. Finding and running are separate packages
on purpose: `internal/acquire` has no way to start a process.

Each tool keeps its own copyright and its own licence. Running a GPL program the
user installed from an MIT-licensed program is ordinary use of that program; it
does not create a combined work, and it places no GPL obligations on this
repository's code.

### Profiles describe these tools; they do not contain or relicense them

A **profile** (`internal/profile`) is a JSON document in this
repository's own format that says which programs a tool provides, what arguments
they take and where its project lives. It contains no third-party code: no
source, no object code, no binary, no vendored fragment. Describing a program is
not distributing it, and a profile is data the Companion reads, not the program
it describes.

Consequently:

- **The profile documents embedded in this binary are this repository's own
  work**, under its MIT licence, whatever the licence of the programs they
  describe. They are compiled in with `//go:embed`; the programs are not.
- **A profile's `license` block states the described program's licence**, not
  this one, and carries that program's notice and its corresponding-source link.
  It is carried in the document so that no code path can handle a tool without
  the licence being visible.
- **A user approving a profile is not receiving a licence grant** and their
  obligations under the described program's licence are unchanged. Approval is
  a decision to let this program run that one.
- **No profile carries a download URL.** A profile may carry the project's
  homepage (`source.homepage`), which the Companion shows as a link for the user
  to follow; it never fetches from it. The `managed_download` route and
  `catalog_package` field of older documents are still read, so those documents
  load, and are never taken.

### ericw-tools 0.18.1

The tool the built-in Quake 1 profile describes.

| | |
| --- | --- |
| Program | **ericw-tools** — `qbsp`, `vis`, `light`, `bspinfo`, `bsputil` |
| Version | **v0.18.1**, released 2018-04-06 |
| Author | Eric Wasylishen, continuing Kevin Shanahan's *tyrutils* |
| Project | <https://ericwa.github.io/ericw-tools/> |
| Source | <https://github.com/ericwa/ericw-tools> |
| Corresponding source for this build | <https://github.com/ericwa/ericw-tools/tree/v0.18.1> |
| Licence of the project's source | **GPL-2.0-or-later** |
| Licence of the distributed binaries | **GPL-3.0-or-later** — see below |
| Also inside the archive | Embree and Intel TBB, **Apache-2.0** |

#### Why the binary's licence is not the source's

The `README.md` shipped inside every v0.18.1 archive states the program's terms
as "either version 2 of the License, or (at your option) any later version", and
then, four lines further down:

> Builds using Embree are licensed under GPLv3+ for compatibility with the
> Apache license.

Every official v0.18.1 binary links Embree — `libembree.so.2` on Linux,
`embree.dll` on Windows, `libembree.2.dylib` on macOS — and every archive ships
`gpl_v3.txt` beside `LICENSE-embree.txt`. So the **project's source** is
GPL-2.0-or-later and the **official build** is conveyed under
GPL-3.0-or-later. `auto-pigeon-tools` recorded the same conclusion
independently, from the files beside its own installation, as
`GPL-3.0-or-later (conveyed with Apache-2.0 components)`.

Both facts are carried in the document rather than in prose here: the profile's
`license.spdx` is `GPL-3.0-or-later`, and its `license.notice` — which a user
is shown where the profile is read — names the GPL-2.0-or-later source terms,
Embree, and the exact version tag the corresponding source is at.

#### The published artifacts

Four archives, published by upstream. The Companion does not download them; a
user gets one from the project's homepage and points the profile at the folder
it unpacked to. The sizes and digests below were measured by downloading each
archive from upstream's release page, and are recorded here as the builds the
profile's claims were measured against.

| Platform | Archive | Size | SHA-256 |
| --- | --- | --- | --- |
| linux/amd64 | `ericw-tools-v0.18.1-Linux.zip` | 14594502 | `986531ff66d692fa732b7f75a6c871dcbd152b98721d1c2475b76d3367f040e2` |
| windows/amd64 | `ericw-tools-v0.18.1-win64.zip` | 12780109 | `a0f39c6faeb29cd08b267880cdcebb310f9938fef4cbbff07d1f6843c36e9cd3` |
| windows/386 | `ericw-tools-v0.18.1-win32.zip` | 6423741 | `562aae414b914ffa8d3a208ca74d16ac4ca2b61031773227c7d4bdc8384b13ef` |
| darwin/amd64 | `ericw-tools-v0.18.1-Darwin.zip` | 9469803 | `efdba039d731702e0ca6ed65f3eec656daa67c0f9f41674e0e842804173d0771` |

All four are at
`https://github.com/ericwa/ericw-tools/releases/download/v0.18.1/`. There is no
arm64 build on any operating system, because upstream published none.

#### Why v0.18.1 and not 2.0 for Quake 1

v0.18.1 is the newest release upstream has **not** marked a pre-release: the
whole 2.0 line is published as `prerelease: true`. It is also the build
`auto-pigeon-tools` pinned as its compiler oracle, so what the Quake 1 profile
describes and what this workspace's acceptance gates are measured against are
the same bytes — the Linux archive's digest above is the archive that
installation was unpacked from.

#### And a second profile, for Quake II: 2.0.0-alpha7

Quake II support exists only in the 2.x line, so there is no non-pre-release to
describe for it. `AUP/AUCOM 215` made that a separate document and a separate
decision rather than moving the Quake 1 line onto a pre-release: two profiles,
and two sets of capability ids.

The described version is **2.0.0-alpha7** (2024-03-17) rather than the newest
alpha, and the reason is the one this repository keeps giving: alpha7 is the
build that was unpacked and run here — `qbsp`, `vis`, `light`, `bspinfo` and
`bsputil`, on a synthetic Quake II map — and every claim the Quake II profile
makes about how those programs behave came from that run. Describing a build
nobody had run would have made the claims about nothing.

Three archives, published by upstream. Each digest was computed from the archive
downloaded from the URL beside it.

| Platform | Archive | Size | SHA-256 |
| --- | --- | --- | --- |
| linux/amd64 | `ericw-tools-2.0.0-alpha7-Linux.zip` | 22898562 | `c87d669c615f92163c21e6e154268c0c2e3de4e78c26b6bd5a2a2e7996a9fe75` |
| windows/amd64 | `ericw-tools-2.0.0-alpha7-win64.zip` | 29065662 | `fa640ce178aa1eef7ae5fc725312826c0ab682945ac58d06999f4e814a30b816` |
| darwin/amd64 | `ericw-tools-2.0.0-alpha7-Darwin.zip` | 41839775 | `ae5bd36cc6b4704067bc95f90aec2c9f4a0582be6b815029b068d476318094b3` |

All three are at
`https://github.com/ericwa/ericw-tools/releases/download/2.0.0-alpha7/`. There
is no 32-bit and no arm64 build on any operating system, because upstream
published none for this line.

The licence conclusion is the same and was reached the same way: the archives
ship `gpl_v3.txt` beside `LICENSE-embree.txt`, and upstream's README says builds
using Embree are GPLv3+. The profile's notice names the GPL-2.0-or-later source
terms, Embree, the `2.0.0-alpha7` tag the corresponding source is at, and — in
those words — that this is a **PRE-RELEASE**.

#### Licence text is not copied here

No GPL or Apache text is vendored into this repository. `gpl_v3.txt` and
`LICENSE-embree.txt` ship inside every archive and land beside the binaries in
the folder the user unpacks; the profile links the canonical text and the
corresponding source. Copying licence text in here would be another copy that
can go stale, and this repository's habit is to name where the authoritative one
is.

### Q3Map2 2.5.17n — described, run, and not downloaded

The Quake III compiler is `q3map2`, from **NetRadiant-custom**'s `20260114`
release. Its own source files carry the GtkRadiant header — *"either version 2
of the License, or (at your option) any later version"* — so it is conveyed
under **GPL-2.0-or-later**. The surrounding tree carries three licences at once
(BSD, LGPL-2.1 and GPL, each file saying which), which is why GitHub's own
detector reports the repository as `Other`; the corresponding source for the
build described here is
`https://github.com/Garux/netradiant-custom/tree/20260114`.

The Companion downloads no program, so this is no longer what sets Q3Map2 apart.
What upstream publishes is still worth recording, because it is why the profile
tells the user to find the program inside the editor they already have:

| Platform | What upstream publishes for `20260114` |
| --- | --- |
| linux/amd64 | `netradiant-custom-20260114-linux-x86_64.7z`, 40181006 bytes, SHA-256 `f48f6f1d0db2b910ef9cb5dc5d8a722852510f3c5c278dc17615c0466b8a7a3d` — one member, `NetRadiant-Custom-x86_64.AppImage` (41036280 bytes), which is the whole map editor |
| windows/amd64 | `netradiant-custom-20260114-windows-x86_64.zip`, 43618125 bytes — the same editor |
| darwin | nothing |

`AUP/AUCOM 216` says not to install another map editor merely because a release
bundles one, and there is no smaller artifact to prefer: `q3map2` resolves
libassimp, libdraco, libminizip, libpugixml, libicu, libxml2 and libglib out of
the bundle's own `../lib`, so it cannot be lifted out on its own. So the profile
declares `user_path` and `system_path`, says all of that where a user reads it,
and **this project neither downloads nor redistributes any of those bytes.**

Two further facts, both established by looking rather than assuming:

- **The published AppImage carries no licence file at all** — no `LICENSE`, no
  `COPYING`, no GPL text anywhere inside it. Whoever redistributes that binary
  has that to answer for; this project does not redistribute it, and the profile
  names the licence, the terms and the corresponding source itself.
- **Everything the Quake III profile claims about the program's behaviour was
  measured** by extracting that AppImage and running `q3map2` — the BSP,
  visibility and lighting stages — against a synthetic Quake III map. The
  version it printed, `2.5.17n-git-68ecbed`, is the version the document names.

### ioquake3 and the generic id Tech 3 profile

Both engine profiles describe **GPL-2.0-or-later** programs this repository does
not contain, download or link against. ioquake3 is `ioquake/ioq3`; the generic
profile describes whatever id-Tech-3-derived engine a user already has, and id
Software's own Quake III Arena engine source is where that vocabulary comes
from.

ioquake3 publishes no GitHub release and no tag, and its builds are unversioned
rolling zips at `files.ioquake3.org`, so there is no single build to describe by
digest either. Both profiles are `user_path`.

**Game data is not covered by any of this.** Quake III Arena's `pak0.pk3` and
the patch `pak1`–`pak8` are id Software's commercial data, not free software,
and upstream says so on its own download page: *"The Quake 3 engine is open
source. The Quake III: Arena game itself is not free. You must purchase the game
to use the data and play Quake 3 with ioquake3."* The Companion never copies,
downloads, fabricates or redistributes it — including in tests, which stand up a
game directory out of a placeholder file of this repository's own.

### Requirement to revisit: bundling a GPL tool

Today **no map-building tool or engine binary is downloaded by the Companion or
shipped in any release archive, installer, or package**, so no GPL material is
redistributed by this project. The one program a release carries besides the
Companion is the proprietary extractor, above, whose own licence file travels
with it and whose licence the bundle manifest names.

**If that changes for a GPL tool** — if its prebuilt binary is ever bundled
inside a release archive, installer, `.deb`/`.rpm`, or `.app` bundle — then that
release becomes a redistribution of GPL software, and it must carry:

- the tool's full licence text and copyright notice, in the same archive,
- a written offer of, or accompanying, corresponding source, per GPL-3.0
  section 6 for the ericw-tools builds above.

The extractor's path through [`build/bundle-manifest.py`](build/bundle-manifest.py)
is the model for it. **This is flagged as a decision to make, not one made
here.**

## Games launched by this application

Games launched through [`internal/launch`](internal/launch/exec.go) are
third-party software installed by the user, under their own licences. This
application starts them as separate processes and redistributes nothing of
theirs.

## Provenance of this repository's own code

`auto-pigeon-companion` absorbed `auto-pigeon-launcher` in
`20260906_203_AUCOM_AUL_Merge-License-And-Repository-Truth`. Both repositories'
entire histories were authored by Andrea D'Intino; neither carried an external
contribution, a vendored tree, or a third-party dependency, so relicensing the
merged result under MIT required no third party's consent. The Launcher's
history is reachable from this repository's `main` through that merge commit.
