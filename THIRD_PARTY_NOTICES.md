# Third-party notices

`auto-pigeon-companion` is MIT licensed, Copyright (c) 2026 Andrea D'Intino.
That licence covers **this repository's own code only** — see [LICENSE](LICENSE).

This document explains the licensing status of everything else the application
touches: what it depends on, what it runs, and what it redistributes. Those are
three different relationships with three different obligations, and they are
kept apart here on purpose.

**A release archive is not under one licence.** It holds three kinds of thing, and each keeps its own
terms:

- **Auto-Pigeon Companion's own code — MIT** (`LICENSE` in this repository).
- **Contract files from `auto-pigeon-libraries` (AULIBS) compiled into the
  Companion — Apache-2.0**, unmodified. See below.
- **The `auto-pigeon-mapio` map converter, shipped beside the Companion — Apache-2.0 AND MIT**: the
  `go/mapio` library and this repository's small wrapper, compiled into one executable. See below.

**One notices file ships inside a release archive**, `NOTICES-auto-pigeon-mapio.txt`, beside the converter
it belongs to. Otherwise an archive carries the Companion, the converter, their `bundle-manifest.json` and
a `SHA256SUMS`, and nothing else (operator decision, 2026-09-25, narrowed by `NEW_371`). The other licence
texts and this file are in the repositories; each archive's `bundle-manifest.json` names the licence of
every file in it, and the release notes say where the texts are.

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
| auto-pigeon-mapio (`go/mapio` from auto-pigeon-libraries, and `cmd/mapio`) | **Apache-2.0 AND MIT** | separate process, shipped as its own file (`auto-pigeon-mapio[.exe]`) beside the Companion in a release bundle | yes: `NOTICES-auto-pigeon-mapio.txt` beside it |
| ericw-tools 2.0.0-alpha11 (qbsp, vis, light, bspinfo, bsputil; the Quake 1 line) | **GPL-3.0-or-later** as distributed (GPL-2.0-or-later source) | separate process, **obtained by the user** from its homepage; nothing is downloaded | no |
| ericw-tools 2.0.0-alpha7 (the Quake II line) | **GPL-3.0-or-later** as distributed (GPL-2.0-or-later source) | separate process, **obtained by the user** from its homepage; nothing is downloaded | no |
| Q3Map2 2.5.17n (NetRadiant-custom `20260114`) | **GPL-2.0-or-later** | separate process, **found by the user**; nothing is downloaded | no |
| ioquake3, and any id Tech 3 engine | **GPL-2.0-or-later** | separate process, already installed | no |
| Games the user launches | the user's own | separate process, already installed | no |

## Go dependencies

The Companion has none: its binary links only the standard library, which is BSD-3-Clause and whose
notice travels with every Go binary. `go.mod` requires exactly one module,
`github.com/auto-pigeon/auto-pigeon-libraries`, and only `cmd/mapio` imports it, so it reaches the converter
beside the Companion and not the Companion itself.

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
(`companion release sbom`), listing this module, its empty linked-module graph, and
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
text is at https://www.apache.org/licenses/LICENSE-2.0, and the AULIBS `NOTICE`
travels with the converter in `NOTICES-auto-pigeon-mapio.txt`.

## auto-pigeon-mapio — Apache-2.0 AND MIT, shipped beside the Companion and never inside it

`auto-pigeon-mapio` is the map converter a release ships. One executable, built in this repository's
release from two parts, each under its own licence:

| part | source | licence |
| --- | --- | --- |
| `go/mapio`, the format library | `auto-pigeon-libraries`, at the exact revision `go.mod` pins | Apache-2.0 |
| `cmd/mapio`, the small wrapper around it | this repository | MIT |

The SPDX expression for the executable is `Apache-2.0 AND MIT`. Neither licence relicenses the other part,
and the archive holding it is never "an MIT archive".

**Its notices travel with it.** Beside the executable, in `dependencies/` (in `Contents/Resources/` inside
a macOS `.app`), is `NOTICES-auto-pigeon-mapio.txt`: this repository's MIT text, the library's `NOTICE`, and
the library's Apache License 2.0 text, assembled by `build/release.sh` from this commit and from the module
revision that was compiled in. It is the one licence file a release archive carries. The rule since
2026-09-25 is that no licence or notice file ships inside an archive; this file is the narrow exception,
because the library is redistributed in binary form and Apache-2.0 §4 requires its licence and NOTICE to
go with it. The bundle manifest's `licenses` list names it.

**It is a separate program.** The Companion finds it in `dependencies/` beside its own executable (in
`Contents/MacOS/` inside a macOS `.app`), checks its digest when the bundle manifest lists it, asks it
`protocol --json`, and invokes it as a **separate operating-system process**. The Companion's own binary
links no part of it, which is why `companion security audit` reports no module dependencies: see
[`internal/converter`](internal/converter/doc.go).

A bundle assembled without one has `"converter": null` and a `converter_absent` entry in its
`bundle-manifest.json`.

### What it replaced

Releases up to `NEW_371` shipped the separately licensed **Auto-Pigeon Extractor** (AUE) in this position.
AUE is proprietary (`LicenseRef-Auto-Pigeon-Proprietary`, Copyright (c) 2026 Andrea D'Intino, all rights
reserved), and builds distributed earlier under AGPL-3.0-only keep that licence. A release built now
carries none of it: no AUE executable, no build dependency on its repository, and no use of a token to
read it. The map-format code it used to hold was published by its owner as `go/mapio` under Apache-2.0;
that covers what moved and nothing else, and the rest of AUE stays proprietary. Earlier release archives
are not changed by any of this, and each keeps the terms it was distributed under.

### The developer override redistributes nothing

`AUCOM_MAPIO_BINARY` names a converter somebody built themselves. Nothing is copied or shipped, and it is
labelled unverified everywhere it appears. It is a development setting, not a distribution.

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
`os/exec`. Communication is process-level only: command-line arguments,
environment, working directory, standard input and output, exit status, and
files on disk.

**Two exceptions are installed by the Companion on the user's behalf** (operator,
2026-10-28; [ADR-0010](docs/adr/0010-the-companion-installs-and-updates-two-official-toolchains.md)):
the Auto-Pigeon builds of ericw-tools (GPL-3.0-or-later as built, with Embree and
oneTBB) and of q3map2 (GPL-2.0-or-later), each fetched **unchanged** from its own
official GitHub release (`auto-pigeon/ericw-tools`, `auto-pigeon/q3map2`) into a
per-user folder of the Companion's own. The archive is unpacked whole, so its
`licenses/` folder — the GPL texts and the notice of every library beside the
programs — stays in the installation; the installation's `install.json` names the
release tag, whose source tree and source archive the same release publishes. The
Companion's own release archives still carry none of them, and nothing here
modifies, links or relicenses them. Every other tool is the user's to install.

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

### ericw-tools 2.0.0-alpha11

The tool the built-in Quake 1 profile describes since 2026-10-07 (HITL). Until
then it described v0.18.1; that section is kept below as the record of what was
measured.

| | |
| --- | --- |
| Program | **ericw-tools** — `qbsp`, `vis`, `light`, `bspinfo`, `bsputil` |
| Version | **2.0.0-alpha11**, published by upstream as a pre-release |
| Author | Eric Wasylishen, continuing Kevin Shanahan's *tyrutils* |
| Project | <https://ericwa.github.io/ericw-tools/> |
| Source | <https://github.com/ericwa/ericw-tools> |
| Corresponding source for this build | <https://github.com/ericwa/ericw-tools/tree/2.0.0-alpha11> |
| Licence of the project's source | **GPL-2.0-or-later** |
| Licence of the distributed binaries | **GPL-3.0-or-later** — for the reason given for v0.18.1 below |
| Also inside the archive | Embree 4 and Intel TBB, **Apache-2.0** |

The Linux build ships `libembree4.so.4`, `libtbb.so.12` and `libtbbmalloc.so.2`
beside the programs — at the top of the folder, with no `bin/` — and carries
`gpl_v3.txt` and `LICENSE-embree.txt`. `auto-pigeon-tools` pins that build as its
compiler oracle and recorded, from the files beside it,
`GPL-3.0-or-later (conveyed with Apache-2.0, unidentified components)`; its
`ericw-tools-contract.json` holds the executables' digests. No other platform's
archive was measured here.

#### Why a 2.0 pre-release for Quake 1

Because it is what Auto-Pigeon's acceptance compiles every map with. `NEW_317`
moved `auto-pigeon-tools`' pinned oracle from v0.18.1 to 2.0.0-alpha11, the
latest upstream release, and on 2026-10-07 HITL moved this profile with it, so
what the Quake 1 profile describes and what the acceptance gates are measured
against are again the same build. Upstream still marks the whole 2.0 line
`prerelease: true`; the profile says so where a user reads it.

### ericw-tools 0.18.1 (described until 2026-10-07)

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

#### Why v0.18.1 and not 2.0 for Quake 1, until 2026-10-07

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

The Companion downloads no program but the two Auto-Pigeon toolchains — and for
q3map2 that is Auto-Pigeon's own build, never this upstream release — so this is
no longer what sets upstream Q3Map2 apart.
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

Today **no map-building tool or engine binary is shipped in any release archive,
installer, or package**. The two Auto-Pigeon toolchains the Companion installs
are fetched from their own official releases, which carry their licence texts
and corresponding source (see *How they are used*); no other GPL material is
fetched, and none is redistributed in the Companion's own release. The one program a release carries besides the
Companion is the `auto-pigeon-mapio` converter, above, whose notices travel
with it and whose licences the bundle manifest names.

**If that changes for a GPL tool** — if its prebuilt binary is ever bundled
inside a release archive, installer, `.deb`/`.rpm`, or `.app` bundle — then that
release becomes a redistribution of GPL software, and it must carry:

- the tool's full licence text and copyright notice, in the same archive,
- a written offer of, or accompanying, corresponding source, per GPL-3.0
  section 6 for the ericw-tools builds above.

The converter's path through [`build/bundle-manifest.py`](build/bundle-manifest.py)
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
