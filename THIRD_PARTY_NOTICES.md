# Third-party notices

`auto-pigeon-launcher` is MIT licensed. That license covers **this repository's
own code only** — see [LICENSE](LICENSE).

This document explains the licensing status of everything else the application
touches.

## Go dependencies

None. The module has no third-party Go dependencies: `go.mod` lists only the Go
version, and there is no `go.sum` because there is nothing to check sums for.
Everything is the standard library.

If a dependency is ever added, its license goes in this document, and it must be
compatible with MIT redistribution.

## External map-building tools (GPL-2.0)

The external tools that build Quake maps are licensed under the **GNU General
Public License, version 2**. They are **not part of this codebase**, and this
repository's MIT license does not apply to them.

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

Each tool keeps its own copyright and its own license. Downloading and running a
GPL-2.0 program from an MIT-licensed program is ordinary use of that program; it
does not create a combined work, and it places no GPL obligations on this
repository's code.

### Per-tool notices — not yet written

**TODO(andrea): which GPL-2.0 tool(s), and which versions, is not decided.**

Once the tools are chosen, each one gets a section here containing:

- the tool's name, version, and upstream project URL,
- its copyright notice, verbatim,
- its full license text, or a file in this repository containing it,
- the URL the binary is downloaded from and its SHA-256 checksum,
- a pointer to the corresponding source, since GPL-2.0 section 3 requires that
  anyone redistributing a binary offer the source that produced it.

Until then, [`internal/tools/manager.go`](internal/tools/manager.go) resolves no
real tools at all: the registry is empty and a fake tool stands in so the
download-and-run pipeline can be exercised.

### Requirement to revisit: bundling versus downloading

Today the design is **download on first run**: no tool binary is shipped in any
release archive, installer, or package, so no GPL-2.0 material is redistributed
by this project.

**If that decision changes** — if a tool's prebuilt binary is ever bundled
directly inside a release archive, installer, `.deb`/`.rpm`, or `.app` bundle —
then that release becomes a redistribution of GPL-2.0 software, and it must
carry:

- the tool's full license text and copyright notice, in the same archive,
- a written offer of, or accompanying, corresponding source, per GPL-2.0
  section 3.

This affects [`internal/tools/manager.go`](internal/tools/manager.go) and every
packaging script under [`build/`](build/). **This is flagged as a decision to
make, not one made here.**

## Games launched by this application

Games launched through `internal/launch` are third-party software installed by
the user, under their own licenses. This application starts them as separate
processes and redistributes nothing of theirs.
