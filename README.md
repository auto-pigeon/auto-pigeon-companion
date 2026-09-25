# Auto-Pigeon Companion

The Auto-Pigeon program that runs on your own computer. It signs in to your
Auto-Pigeon account, builds Quake maps with map-building tools you install
yourself, inspects maps through the Auto-Pigeon Extractor that ships beside it,
and starts games — including joining a game somebody else is hosting.

It is one executable with no installer. Started with no arguments it opens its
interface in the browser you already have; every command it offers is also
available from a terminal.

> **Quake 1 is the supported path. Quake II and Quake III are work in
> progress.** No Quake engine has been run by this project; the engine profiles
> it ships are marked `unverified` until one is.

- [Download and run](#download-and-run)
- [Using it](#using-it)
- [Command line](#command-line)
- [Configuration](#configuration)
- [Removing it](#removing-it)
- [Building from source](#building-from-source)
- [Licence](#licence)

## Download and run

Every push to `main` publishes a development prerelease, `v1.<commit count>`, on
the repository's **Releases** page. Pick the archive for your system:

| System | Archive |
| --- | --- |
| Linux, x86-64 | `auto-pigeon-companion-<version>-linux-amd64.zip` |
| Linux, ARM64 | `auto-pigeon-companion-<version>-linux-arm64.zip` |
| Windows, x86-64 | `auto-pigeon-companion-<version>-windows-amd64.zip` |
| macOS, Apple silicon | `auto-pigeon-companion-<version>-darwin-arm64.zip` |
| macOS, Intel | `auto-pigeon-companion-<version>-darwin-amd64.zip` |

`SHA256SUMS` beside the archives lets you check a download:

```console
$ sha256sum -c SHA256SUMS --ignore-missing
auto-pigeon-companion-1.158-linux-amd64.zip: OK
```

That makes a corrupted or truncated download detectable, which is not a
signature: anybody who can replace the archives can replace the checksum file.
There is no Apple Developer ID and no Authenticode certificate for this project,
so macOS shows a Gatekeeper warning and Windows shows SmartScreen the first time
you run it.

An archive holds the program and nothing it does not need to run:

```text
auto-pigeon-companion-1.159-linux-amd64/
  companion                       the Companion (companion.exe on Windows)
  SHA256SUMS                      digests for `sha256sum -c` (not in the macOS archive)
  dependencies/
    auto-pigeon-extractor         the Auto-Pigeon Extractor, a separate program the Companion runs to read maps
    bundle-manifest.json          the SHA-256 of every file; the Companion checks the extractor against it
```

On macOS the archive holds `Auto-Pigeon Companion.app`, with both programs in
`Contents/MacOS/` and the manifest in `Contents/Resources/`. `companion.exe`
and the `.app` carry the Auto-Pigeon icon; a Linux executable has no icon.

Unpack it anywhere and start it:

```console
$ unzip auto-pigeon-companion-1.158-linux-amd64.zip
$ cd auto-pigeon-companion-1.158-linux-amd64
$ ./companion
Auto-Pigeon Companion 1.158 is open in your browser.
Close its last window to stop it, or press Ctrl+C.
```

On Windows, double-click `companion.exe` (SmartScreen: **More info › Run
anyway**). On macOS, open `Auto-Pigeon Companion.app`; the first time, macOS
blocks it until you allow it in **System Settings › Privacy & Security**.

Check what you are running, and that the extractor beside it is the one the
release shipped:

```console
$ ./companion version
1.158
commit 6ab65f3c4c071df866ac6150a4b6dc956e001527 (clean)
built with go1.26.8 for linux/amd64, CGO_ENABLED=0
$ ./companion extractor status
extractor: shipped with this Companion, checked against its bundle manifest
```

The Companion downloads no program. Map-building tools and game engines are
ones you install yourself; the Companion finds them in a folder you choose, on
your `PATH`, or in a game's own installation.

## Using it

The page has seven areas, in the order of a first run:

| Area | What it is for |
| --- | --- |
| **Build & Run** | Sign in, choose a map revision, a build profile and an engine, review, and press one button. |
| **My Maps** | Your maps in your Auto-Pigeon account, and the revisions this computer has downloaded and verified. |
| **Build** | Compiling a map: the pipeline, the map, the exact command per stage, then the output. |
| **Run** | Starting a game: which engine, where it is, which map, and the exact command. |
| **Profiles** | What a tool or engine asks to be allowed to do, approving it, and writing your own. |
| **Jobs** | Everything that has run, with its command, exit status and output. |
| **Settings** | Which Auto-Pigeon you use, and where the Companion keeps things. |

**Signing in.** **Settings** and the **Sign in** dialog offer the two official
deployments by name — **Auto-Pigeon** (`https://auto-pigeon.com`) and
**Auto-Pigeon beta** (`https://beta.auto-pigeon.com`). Nothing is contacted
until you pick one.

**Tools.** For Quake 1 the qualified compiler is ericw-tools **v0.18.1**.
Download it from its homepage (the profile's **Homepage** button), unpack it,
then in **Profiles › Configure › ericw-tools** choose that folder. From a
terminal, with the profile document from this repository:

```console
$ companion acquire resolve internal/profile/builtin/ericw-tools-q1.tool.json \
    --mode user_path --user-path ~/tools/ericw-tools-v0.18.1 --bind
```

A profile only runs after you approve what it does (**Profiles**, or
`companion toolchain grant`).

**Stopping.** Closing the Companion's last browser window stops it, 15 seconds
later, so a reload does not. A build or a game you started keeps it running
until that finishes. **Quit Auto-Pigeon Companion…** in the cog menu, or Ctrl+C
in the terminal, stops it at once. `companion --stay-running` keeps it running
with no window open.

**Joining a game.** **Live Games** lists games people are hosting. A join link
is an `autopigeon://` URL, and the operating system needs telling that the
Companion opens it:

```console
$ ./companion uri status
$ ./companion uri register
$ ./companion uri unregister
```

The command is `game open` with **no approval flag at all**: opening a link
checks its shape, shows it in the Companion and starts nothing. Joining is a
separate step you take after reviewing the exact command the page shows.

**Hosting a game.** A hosted game starts **Private**. Choosing **Public** shows
**Help to connect**, which explains the manual UDP port forwarding people
outside your network need to reach you.

## Command line

Everything the page can do, the command line can do too.

```console
$ companion --help
companion — Auto-Pigeon Companion: build Quake maps, inspect them, and launch games

usage:
  companion   start the local GUI and open a browser; closing its last window stops it
  companion --stay-running  the same, but keep running when every window is closed
  companion --debug   the GUI with the developer controls unlocked
  companion <command> [arguments]

commands:
  serve [--port <n>] [--open] [--debug] [--interactive | --stay-running]   run the local GUI server; it keeps running until stopped unless --interactive
  auth login [--email <address>] | status | logout   authenticate against auto-pigeon-backend
  aub capabilities | catalog | show | revisions | sync | cached | verify | export | clean   browse auto-pigeon-backend's assets and sync exact revisions to this machine
  job run | preview | list | show | logs | cancel | retry | artifacts | profiles   run a profile action as a supervised job, and inspect what ran
  build run | preview | list | show | pipelines   build a map through a pipeline: several supervised jobs, wired, with a manifest
  package targets | preview | create | inspect | verify | extract   build a PAK or PK3 from what a build produced, and read one somebody else made
  toolchain validate | show | canonicalize | digest | diff | list | schema | review | grant | withdraw | homepage  read, check and compare tool, engine and pipeline toolchains, and approve one to run
  acquire resolve   find a toolchain's programs on this machine (a folder, PATH, or a game's own copy); nothing is downloaded
  engine list | show | detect | bind | check | preview | run | stage | unstage   set up a Quake engine you already have, and start it as a supervised job
  game list | show | link | join | preview | host | stop   find a game somebody is hosting and join it, or advertise one of your own
  launch (retired)   retired: it read a placeholder launch config; use `engine run <profile> --action play_map`
  extractor status | version | convert <file.apmap>   the separately licensed auto-pigeon-extractor (AUE) shipped beside this program
  feedback compatibility --game <family> --summary <text> [--share <what>]   report that a work-in-progress game did not do what you expected — nothing is attached unless you say so
  uri status | register | unregister   see, set or remove this machine's handler for autopigeon:// links
  security matrix | residual | audit   the threat model, the risks accepted with it, and what this build is made of
  release sbom | checksums   the documents a release ships beside its binaries
  uninstall [--purge --confirm]   show what this program keeps on this machine, and delete it
  acceptance run | verify | lanes | schema | fixture | noise   the native operator acceptance kit, on the machine an artifact is for
  dev fault job|readiness [--correlation-id <id>] [--aub <url>] [--bound <duration>]   end-to-end test controls; refused unless AUCOM_E2E_FAULTS=1
  migrate   fold Launcher and older Companion configuration into the current one
  version   print the build version
```

Each command prints its own usage with no arguments (`companion game`,
`companion toolchain`, …). Some common ones:

```console
$ companion auth login --email you@example.com      # password prompt, or AUCOM_PASSWORD
$ companion toolchain list                           # every tool and engine profile
$ companion toolchain show my-profile.json         # what a profile asks for, and its digest
$ companion toolchain validate my-profile.json
$ companion toolchain diff old.json new.json
$ companion toolchain schema                         # the published JSON Schemas
$ companion engine list                              # engines, and whether each is bound
$ companion engine bind auto-pigeon.engine.quakespasm --engine /opt/quakespasm/quakespasm
$ companion game list                                # games being hosted now
$ companion game join <link>                         # shows the exact command; --approve launches
$ companion extractor convert map.apmap
$ companion security matrix                          # the threat model
$ companion security residual                        # the risks accepted with it
$ companion security audit                           # what this build is made of
$ companion release sbom --out sbom.cdx.json
$ companion release checksums --dir dist --out -
$ companion uninstall                                # what it keeps on this computer
```

### `companion profile …` still works

`toolchain` was called `profile` before; `companion profile list` and every
other `profile` spelling still resolve to the same commands.

## Configuration

Settings are saved in a per-user `config.json`, written with mode 0600 because
it holds your Auto-Pigeon session:

| System | Path |
| --- | --- |
| Linux | `~/.config/auto-pigeon-companion/config.json` |
| macOS | `~/Library/Application Support/auto-pigeon-companion/config.json` |
| Windows | `%AppData%\auto-pigeon-companion\config.json` |

Job records and downloaded map revisions live in the matching cache directory.

To use another backend — a local development stack, say — start with
`companion --debug` (Settings then accepts any address), or put a `config.json`
holding only `aub_base_url` and `port` beside the executable, or set:

| Variable | Purpose |
| --- | --- |
| `AUCOM_AUB_BASE_URL` | the Auto-Pigeon backend address |
| `AUCOM_PORT` | the port of the local page |
| `AUCOM_PASSWORD` | the password for a scripted `auth login` |
| `AUCOM_OFFLINE` | `1` forbids every network access |
| `AUCOM_JOBS_DIR`, `AUCOM_PROFILES_DIR`, `AUCOM_ASSET_CACHE_DIR` | where jobs, imported profiles and synced assets live |
| `AUCOM_AUE_BINARY` | a development build's extractor instead of the bundled one; shown as **unverified** everywhere |
| `AUCOM_ENV_FILE` | a `.env` to read instead of `./.env` (development only) |

```console
$ AUCOM_AUB_BASE_URL=http://localhost:9190 ./companion auth status
aub: http://localhost:9190
signed in: no
```

## Removing it

Delete the unpacked folder. To remove what the Companion kept on this computer —
settings, session, approvals, job history, caches — and the `autopigeon://`
handler, look first, then confirm:

```console
$ ./companion uninstall
$ ./companion uninstall --purge --confirm
```

It never deletes a folder it did not create.

## Building from source

You need **Go 1.26.8** (`go.mod`; with the default `GOTOOLCHAIN=auto` an older
`go` fetches it) and Git. There are no other dependencies: `go.mod` lists no
third-party module, and the page is plain HTML, CSS and JavaScript embedded in
the binary.

```console
$ git clone https://github.com/auto-pigeon/auto-pigeon-companion.git
$ cd auto-pigeon-companion
$ go build -ldflags "-X main.version=1.$(git rev-list --count HEAD)" -o companion ./cmd/companion
$ ./companion
```

The binary is CGO-free and cross-compiles to all six targets:

```console
$ GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o companion.exe ./cmd/companion
$ GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o companion ./cmd/companion
```

A source build has no extractor beside it, so map inspection is unavailable
until you point it at one you built from the extractor's repository:

```console
$ AUCOM_AUE_BINARY=../auto-pigeon-extractor/bin/auto-pigeon-extractor ./companion extractor status
extractor: UNVERIFIED developer override
```

**Checks** — all three must pass before a change is merged:

```console
$ gofmt -l .
$ go vet ./...
$ go test ./...
```

**Release archives.** `build/release.sh` builds all six targets, the macOS
`.app`, an SBOM and `SHA256SUMS` into `dist/`. It puts the Auto-Pigeon icon on
the Windows executables and the `.app`, generated by `build/icon` from the
page's 128×128 mark in `internal/web/assets/brand/`:

```console
$ ./build/release.sh --out dist/local --targets linux/amd64,windows/amd64
```

The published release is made by `.github/workflows/release.yml` on every push
to `main`: it tests on Linux, Windows and macOS, builds the extractor from the
commit pinned in `build/aue-pin.json`, puts it beside the Companion in each
archive, runs each archive on a native runner, and publishes the prerelease.

**Native acceptance** on hardware CI does not have runs from a checkout against
an unpacked archive, and writes a result bundle to send back:

```console
$ acceptance/run-acceptance.sh --companion ~/auto-pigeon-companion-1.158-linux-arm64/companion
```

(`acceptance\run-acceptance.ps1 -Companion …\companion.exe` on Windows.)

There is no desktop window beyond your browser; why is recorded in
`$MAPPER_ROOT/LLM/docs/adr/0026-the-companion-desktop-shell-is-deferred-behind-a-measured-cgo-blocker.md`.

## Licence

The Companion's own code is MIT — see [LICENSE](LICENSE), Copyright (c) 2026
Andrea D'Intino. A release archive holds more than this code, and each part
keeps its own terms:

| Part | Licence |
| --- | --- |
| Auto-Pigeon Companion's own code | MIT |
| Contract files from auto-pigeon-libraries compiled into it | Apache-2.0 |
| Auto-Pigeon Extractor, shipped beside it | proprietary (`LicenseRef-Auto-Pigeon-Proprietary`), all rights reserved; using it needs the copyright owner's written authorization |
| Map-building tools, engines and games you install | their own |

No licence file ships inside an archive; each archive's `bundle-manifest.json`
names the licence of every file, and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)
sets out the details.

The Auto-Pigeon mark — the page's logo and the executables' icon — is the
project owner's artwork. It is not licensed under the MIT licence by being built
into the program.
