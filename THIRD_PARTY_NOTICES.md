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
| auto-pigeon-extractor (AUE) | **AGPL-3.0** | separate process, **embedded in release builds** | **yes, in a release build** |
| ericw-tools 0.18.1 (qbsp, vis, light, bspinfo, bsputil) | **GPL-3.0-or-later** as distributed (GPL-2.0-or-later source) | separate process, downloaded at runtime | no |
| Games the user launches | the user's own | separate process, already installed | no |

## Go dependencies

None. The module has no third-party Go dependencies: `go.mod` lists only the Go
version, and there is no `go.sum` because there is nothing to check sums for.
Everything is the standard library, which is BSD-3-Clause and whose notice
travels with every Go binary.

If a dependency is ever added, its licence goes in this document, and it must be
compatible with MIT redistribution.

## auto-pigeon-extractor (AUE) — AGPL-3.0, and the open decision about it

AUE is a **separate program under the GNU Affero General Public License,
version 3**. It is not part of this codebase and this repository's MIT licence
does not apply to it, does not relicense it, and cannot.

### How it is used

The Companion invokes AUE as a **separate operating-system process** through
`os/exec` and reads its stdout — see [`internal/aue`](internal/aue/runner.go).
Communication is process-level only: subcommand, arguments, environment,
working directory, standard output, exit status. AUE's own packages all live
under `internal/`, so Go's internal-package rule makes importing them
impossible even by accident, and AUE never appears in `go.mod`.

### The part that needs a decision

`internal/aue/embed.go` is written for a build that **copies the
platform-matching AUE binary into the Companion executable with `//go:embed`**.
A release built that way *redistributes AGPL-3.0 software inside an
MIT-licensed binary*.

Two facts about that, kept separate because they are separate:

1. **It is not a combined work.** Embedded bytes that are written to a
   temporary file and executed as their own process are at the same arm's
   length as two programs shipped in one archive. Nothing links, and nothing
   shares an address space. AUE's copyleft therefore does not reach this
   repository's code, and the Companion stays MIT.
2. **The distribution obligations still apply, in full.** Shipping an AGPL-3.0
   program means the release must carry AUE's full licence text and its
   copyright notice, and must offer the corresponding source for exactly the
   AUE build it contains — AGPL-3.0 section 6. AUE's own third-party notices
   (its Go dependencies, which are compiled into it) travel with it.

**As of this writing no release does either, because no release embeds AUE
yet.** `internal/aue/embedded/` contains only `.gitkeep`, CI builds without an
extractor, and such a binary reports the extractor as unavailable at runtime.
A developer points at a local build with `AUCOM_AUE_BINARY`, which redistributes
nothing.

**TODO(andrea): decide before the first release that embeds AUE.** The options
are:

- **Embed it**, and add to every release artifact: AUE's `LICENSE`, its
  copyright notice, its own third-party notices, the exact AUE commit or tag,
  and a written offer of corresponding source. `build/macos/make-app-bundle.sh`,
  `build/linux/nfpm.yaml` and `build/windows/installer.iss` all need entries.
- **Download it at first run**, the way the map-building tools below are
  handled, which redistributes nothing and reduces this section to the
  process-boundary paragraph above.

This is flagged as a decision to make, not one made here.

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
[`catalog/ericw-tools-q1.catalog.json`](catalog/ericw-tools-q1.catalog.json).
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

#### Why v0.18.1 and not 2.0

At the time this entry was written, v0.18.1 was still the newest release
upstream had **not** marked a pre-release: the whole 2.0 line, up to
`2.0.0-alpha11` (2026-06-05), is published as `prerelease: true`. It is also the
build `auto-pigeon-tools` pinned as its compiler oracle, so what the Companion
downloads and what this workspace's acceptance gates are measured against are
the same bytes — the Linux archive's digest above is the archive that
installation was unpacked from. A profile for the 2.0 line is a separate
document and a separate decision.

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
