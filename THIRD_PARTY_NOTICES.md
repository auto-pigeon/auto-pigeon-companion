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
| External map-building tools | GPL-2.0 | separate process, downloaded at runtime | no |
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

## External map-building tools (GPL-2.0)

The external tools that build Quake maps are licensed under the **GNU General
Public License, version 2**. They are **not part of this codebase**, and this
repository's MIT licence does not apply to them.

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

No Go dependency may pull a GPL-2.0 tool's source or object code into this
module. This is an architectural constraint, enforced in
[`internal/tools`](internal/tools/manager.go), whose package documentation
restates it, and asserted by a test that fails if the tool registry gains an
entry without this document being updated.

Each tool keeps its own copyright and its own licence. Downloading and running a
GPL-2.0 program from an MIT-licensed program is ordinary use of that program; it
does not create a combined work, and it places no GPL obligations on this
repository's code.

### Per-tool notices — not yet written

**TODO(andrea): which GPL-2.0 tool(s), and which versions, is not decided.**

Once the tools are chosen, each one gets a section here containing:

- the tool's name, version, and upstream project URL,
- its copyright notice, verbatim,
- its full licence text, or a file in this repository containing it,
- the URL the binary is downloaded from and its SHA-256 checksum,
- a pointer to the corresponding source, since GPL-2.0 section 3 requires that
  anyone redistributing a binary offer the source that produced it.

Until then, [`internal/tools/manager.go`](internal/tools/manager.go) resolves no
real tools at all: the registry is empty and a fake tool stands in so the
download-and-run pipeline can be exercised. The fake tool is this repository's
own code and is not a third-party program.

### Requirement to revisit: bundling versus downloading

Today the design is **download on first run**: no tool binary is shipped in any
release archive, installer, or package, so no GPL-2.0 material is redistributed
by this project.

**If that decision changes** — if a tool's prebuilt binary is ever bundled
directly inside a release archive, installer, `.deb`/`.rpm`, or `.app` bundle —
then that release becomes a redistribution of GPL-2.0 software, and it must
carry:

- the tool's full licence text and copyright notice, in the same archive,
- a written offer of, or accompanying, corresponding source, per GPL-2.0
  section 3.

This affects [`internal/tools/manager.go`](internal/tools/manager.go) and every
packaging script under [`build/`](build/). **This is flagged as a decision to
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
