# Third-party notices

`auto-pigeon-companion` is MIT licensed. That licence covers **this
repository's own code only** — see [LICENSE](LICENSE).

This document explains the licensing status of everything else the application
touches: what it depends on, what it runs, and what it redistributes. Those are
three different relationships with three different obligations, and they are
kept apart here on purpose.

## Summary

| Component | Licence | Relationship | Redistributed by us? |
| --- | --- | --- | --- |
| This repository's code | MIT | is the product | yes |
| Go standard library | BSD-3-Clause | compiled in | yes, as part of the binary |
| Go module dependencies | — | none exist | — |
| auto-pigeon-extractor (AUE) | **AGPL-3.0-only** | separate process, downloaded at runtime against a signed catalogue | no |
| ericw-tools 0.18.1 (qbsp, vis, light, bspinfo, bsputil) | **GPL-3.0-or-later** as distributed (GPL-2.0-or-later source) | separate process, downloaded at runtime | no |
| ericw-tools 2.0.0-alpha7 (the Quake II line) | **GPL-3.0-or-later** as distributed (GPL-2.0-or-later source) | separate process, downloaded at runtime | no |
| Q3Map2 2.5.17n (NetRadiant-custom `20260114`) | **GPL-2.0-or-later** | separate process, **found by the user**; nothing is downloaded | no |
| ioquake3, and any id Tech 3 engine | **GPL-2.0-or-later** | separate process, already installed | no |
| Games the user launches | the user's own | separate process, already installed | no |

## Go dependencies

None. The module has no third-party Go dependencies: `go.mod` lists only the Go
version, and there is no `go.sum` because there is nothing to check sums for.
Everything is the standard library, which is BSD-3-Clause and whose notice
travels with every Go binary.

If a dependency is ever added, its licence goes in this document, and it must be
compatible with MIT redistribution.

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
every external program the Companion can obtain — each with the relationship it
has to the artifact, because a component somebody downloads later is not a
component nobody should be told about.

## auto-pigeon-extractor (AUE) — AGPL-3.0-only, and why no release contains it

AUE is a **separate program under the GNU Affero General Public License,
version 3**. It is not part of this codebase, this repository's MIT licence does
not apply to it, does not relicense it, and cannot.

**No release of the Companion contains it.** That is the answer to what used to
be an open decision here, and it was settled by `AUE/AUB/AUCOM 211`.

### The identifier is `AGPL-3.0-only`

Not `-or-later`. That election belongs to the copyright holder and is made by
saying so; the extractor's repository ships the plain AGPLv3 text and elects no
later version anywhere. The extractor declares the identifier ITSELF — run
`auto-pigeon-extractor protocol` — and every document here carries that answer
rather than restating it, because a program under a different licence must not
be the thing that says what this one is licensed as: it ships a copy of the
claim, the copy goes stale, and the stale copy is the one a user reads.

### How it is used

The Companion **downloads** it, against the same signed, revocable catalogue the
map-building tools come through, at the version a signed compatibility manifest
names for this Companion on this platform — and then invokes it as a **separate
operating-system process** through `os/exec` and reads its stdout. See
[`internal/aue`](internal/aue/doc.go).

Communication is process-level only: subcommand, arguments, environment, working
directory, standard output, exit status. AUE's own packages all live under
`internal/`, so Go's internal-package rule makes importing them impossible even
by accident, and AUE never appears in `go.mod`.

### What that means for distribution, precisely

**Downloading redistributes nothing.** The bytes come from their publisher to
the user's machine; this program is not in the chain of distribution any more
than a package manager's index is. The obligations of AGPL-3.0 section 6 fall on
whoever publishes the artifacts, which is that project.

Two things are nevertheless enforced here rather than assumed:

1. **The corresponding-source offer travels with the entry.** The catalogue
   refuses an `AGPL-`/`GPL-` package that names no corresponding source, in
   `catalog.Package.validate`. So a user who is about to download it is shown
   where the source for exactly that build is, before anything is fetched.
2. **The licence is shown, and the aggregation sentence with it.**
   `companion extractor install` prints the SPDX identifier, the
   corresponding-source URL, and the fixed sentence saying that running a
   program as a subprocess does not make it part of the program that ran it.

### What was wrong with embedding it, since the code for that existed

`internal/aue/embed.go` used to copy the platform-matching AUE binary into the
Companion executable with `//go:embed`, and a release built that way would have
redistributed AGPL-3.0 software inside an MIT-licensed binary. It was never
released — `internal/aue/embedded/` only ever contained `.gitkeep` — and the
mechanism is now gone, along with the build steps that staged it. CI fails if it
comes back: see the `no-embedded-extractor` job.

Two facts about that arrangement, kept separate because they are separate and
because they still describe the boundary correctly:

1. **It would not have been a combined work.** Embedded bytes written to a
   temporary file and executed as their own process are at the same arm's length
   as two programs shipped in one archive. Nothing links, and nothing shares an
   address space. AUE's copyleft did not reach this repository's code, and the
   Companion stayed MIT.
2. **The distribution obligations would have applied, in full.** Shipping an
   AGPL-3.0 program means carrying its full licence text and copyright notice,
   and offering the corresponding source for exactly the build shipped —
   AGPL-3.0 section 6 — together with its own third-party notices. Nothing here
   did that, which is the second reason the arrangement had to go.

### The developer override redistributes nothing

`AUCOM_AUE_BINARY` points at a build the developer already has. It is local, it
is labelled unverified everywhere it appears, and nothing produced with it is
uploaded or published.

## External map-building tools (GPL)

The external tools that build Quake maps are licensed under the **GNU General
Public License**. They are **not part of this codebase**, and this repository's
MIT licence does not apply to them.

Which version of the GPL is a question with a measured answer rather than an
assumed one, and the two halves differ — see the per-tool section below.

### How they are used

They are downloaded as **independent, prebuilt binaries** and invoked as
**separate operating-system processes** via `os/exec`. Communication is
process-level only: command-line arguments, environment, working directory,
standard input and output, exit status, and files on disk.

They are never:

- compiled into this binary,
- statically or dynamically linked against this binary,
- vendored into this repository,
- embedded with `//go:embed`,
- or reached through any in-process calling convention.

No Go dependency may pull a GPL tool's source or object code into this
module. This is an architectural constraint, split across two packages that
each restate it in their own documentation:
[`internal/catalog`](internal/catalog/doc.go) says which bytes a tool is,
[`internal/acquire`](internal/acquire/doc.go) obtains and verifies them, and
[`internal/job`](internal/job/exec.go) runs the result — as an executable path
and an argument array handed to `os/exec`, never through a shell and never
through an in-process call. Acquisition and execution are separate packages on
purpose: neither can grow into the other, and `internal/acquire` has no way to
start a process.

Each tool keeps its own copyright and its own licence. Downloading and running a
GPL program from an MIT-licensed program is ordinary use of that program; it
does not create a combined work, and it places no GPL obligations on this
repository's code.

### Profiles describe these tools; they do not contain or relicense them

A **profile** ([README](README.md#profiles)) is a JSON document in this
repository's own format that says which programs a tool provides, what arguments
they take and where to obtain them. It contains no third-party code: no source,
no object code, no binary, no vendored fragment. Describing a program is not
distributing it, and a profile is data the Companion reads, not the program it
describes.

Consequently:

- **The profile documents embedded in this binary are this repository's own
  work**, under its MIT licence, whatever the licence of the programs they
  describe. They are compiled in with `//go:embed`; the programs are not.
- **A profile's `license` block states the described program's licence**, not
  this one, and carries that program's notice and — where a copyleft licence
  requires it for a binary offered for download — the corresponding-source link.
  It is carried in the document so that no code path can handle a tool without
  the licence being visible, and so the acquisition path can show it before
  anything is fetched.
- **A user approving a profile is not receiving a licence grant** and their
  obligations under the described program's licence are unchanged. Approval is
  a decision to let this program run that one.
- **No profile carries a download URL.** Managed downloads name an entry in a
  signed acquisition catalogue, which holds the URL, size, digest, upstream
  source, licence and corresponding-source offer. That keeps the redistribution
  question in one place rather than scattered across every document that points
  at a build.

### ericw-tools 0.18.1

The one tool this project publishes a catalogue entry for.

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
GPL-2.0-or-later and the **build this catalogue points at** is conveyed under
GPL-3.0-or-later. `auto-pigeon-tools` recorded the same conclusion
independently, from the files beside its own installation, as
`GPL-3.0-or-later (conveyed with Apache-2.0 components)`.

Both facts are carried in the documents rather than in prose here: the profile's
and the catalogue entry's `license.spdx` is `GPL-3.0-or-later`, and their
`license.notice` — which a user is shown, and must acknowledge, before anything
is downloaded — names the GPL-2.0-or-later source terms, Embree, and the exact
version tag the corresponding source is at.

#### The published artifacts

Four archives, pinned by exact size and SHA-256 in
[`catalog/ericw-tools.catalog.json`](catalog/ericw-tools.catalog.json).
Each digest was measured by downloading the archive from the URL beside it.

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
`auto-pigeon-tools` pinned as its compiler oracle, so what the Companion
downloads and what this workspace's acceptance gates are measured against are
the same bytes — the Linux archive's digest above is the archive that
installation was unpacked from.

#### And a second entry, for Quake II: 2.0.0-alpha7

Quake II support exists only in the 2.x line, so there is no non-pre-release to
pin for it. `AUP/AUCOM 215` made that a separate document and a separate
decision rather than moving the Quake 1 line onto a pre-release: the two entries
are two packages in one catalogue, two profiles, and two sets of capability ids.

The pinned version is **2.0.0-alpha7** (2024-03-17) rather than the newest
alpha, and the reason is the one this repository keeps giving: alpha7 is the
build that was unpacked and run here — `qbsp`, `vis`, `light`, `bspinfo` and
`bsputil`, on a synthetic Quake II map — and every claim the Quake II profile
makes about how those programs behave came from that run. Pinning a build
nobody had run would have made the claims about nothing.

Three archives, pinned by exact size and SHA-256 in the same catalogue. Each
digest was computed from the archive downloaded from the URL beside it.

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
using Embree are GPLv3+. The notice a user acknowledges before downloading names
the GPL-2.0-or-later source terms, Embree, the `2.0.0-alpha7` tag the
corresponding source is at, and — in those words — that this is a
**PRE-RELEASE**.

#### Licence text is not copied here

No GPL or Apache text is vendored into this repository. `gpl_v3.txt` and
`LICENSE-embree.txt` ship inside every archive and land in the tool cache with
the binaries; the catalogue links the canonical text and the corresponding
source, and the notice a user acknowledges names both. Copying licence text in
here would be a fourth copy that can go stale, and this repository's habit is to
name where the authoritative one is.

#### There is still no signing key

The catalogue payload is in the repository; **no catalogue signing key exists
and no signed catalogue is published**, so nothing downloads by default. The
acquisition path is exercised end to end against an in-process HTTPS fixture
serving archives the tests build themselves — this repository's own bytes, under
its own licence — and, for the real chain, against a locally signed copy of the
payload above. The test signing keys under
[`internal/catalog/testdata`](internal/catalog/testdata) are generated fixtures
and sign nothing outside the tests.

### Q3Map2 2.5.17n — described, run, and deliberately not downloaded

The Quake III compiler is `q3map2`, from **NetRadiant-custom**'s `20260114`
release. Its own source files carry the GtkRadiant header — *"either version 2
of the License, or (at your option) any later version"* — so it is conveyed
under **GPL-2.0-or-later**. The surrounding tree carries three licences at once
(BSD, LGPL-2.1 and GPL, each file saying which), which is why GitHub's own
detector reports the repository as `Other`; the corresponding source for the
build described here is
`https://github.com/Garux/netradiant-custom/tree/20260114`.

**Nothing about it is in the acquisition catalogue, and that is the notable
part.** Both EricW entries are pinned by digest and downloaded on demand. This
one is not, and the reason is what upstream publishes rather than a policy
difference:

| Platform | What upstream publishes for `20260114` |
| --- | --- |
| linux/amd64 | `netradiant-custom-20260114-linux-x86_64.7z`, 40181006 bytes, SHA-256 `f48f6f1d0db2b910ef9cb5dc5d8a722852510f3c5c278dc17615c0466b8a7a3d` — one member, `NetRadiant-Custom-x86_64.AppImage` (41036280 bytes), which is the whole map editor |
| windows/amd64 | `netradiant-custom-20260114-windows-x86_64.zip`, 43618125 bytes — the same editor |
| darwin | nothing |

`AUP/AUCOM 216` says not to install another map editor merely because a release
bundles one, and there is no smaller artifact to prefer: `q3map2` resolves
libassimp, libdraco, libminizip, libpugixml, libicu, libxml2 and libglib out of
the bundle's own `../lib`, so it cannot be lifted out on its own. This program
also unpacks zip and tar.gz only, not 7z. So the profile declares `user_path`
and `system_path`, says all of that where a user reads it, and **this project
neither downloads nor redistributes any of those bytes.**

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

There is nothing to pin for ioquake3 either, and again for a reason of
upstream's: it publishes no GitHub release and no tag, and its builds are
unversioned rolling zips at `files.ioquake3.org`. A catalogue entry names a size
and a digest, and a URL whose contents change has neither. Both profiles are
`user_path`.

**Game data is not covered by any of this.** Quake III Arena's `pak0.pk3` and
the patch `pak1`–`pak8` are id Software's commercial data, not free software,
and upstream says so on its own download page: *"The Quake 3 engine is open
source. The Quake III: Arena game itself is not free. You must purchase the game
to use the data and play Quake 3 with ioquake3."* The Companion never copies,
downloads, fabricates or redistributes it — including in tests, which stand up a
game directory out of a placeholder file of this repository's own.

### Requirement to revisit: bundling versus downloading

Today the design is **download on first run**: no tool binary is shipped in any
release archive, installer, or package, so no GPL material is redistributed by
this project.

**If that decision changes** — if a tool's prebuilt binary is ever bundled
directly inside a release archive, installer, `.deb`/`.rpm`, or `.app` bundle —
then that release becomes a redistribution of GPL software, and it must carry:

- the tool's full licence text and copyright notice, in the same archive,
- a written offer of, or accompanying, corresponding source, per GPL-3.0
  section 6 for the ericw-tools builds above.

This affects [`internal/acquire`](internal/acquire/doc.go) and every packaging
script under [`build/`](build/). **This is flagged as a decision to
make, not one made here.**

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
