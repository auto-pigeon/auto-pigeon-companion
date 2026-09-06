# Auto-Pigeon Companion

The local Auto-Pigeon runtime: one MIT-licensed desktop application that signs
in against **AUB** ([auto-pigeon-backend][aub], a PocketBase instance), builds
Quake maps with external map-building tools, inspects them by driving **AUE**
([auto-pigeon-extractor][aue]) as a subprocess, and launches games.

It runs as a local web app in the browser you already have, or headless from
the command line. Both surfaces are the same binary.

> **Status: structural bootstrap.** The architecture, package boundaries, build
> tooling and packaging are real and tested. The map operations themselves are
> not implemented yet: `build` drives a fake tool because no external tool has
> been chosen, and launch configurations come from a local stub because AUB's
> schema is not confirmed. Every such placeholder is marked in the source at
> the point it will be replaced.

This repository absorbed **auto-pigeon-launcher** (AUL) in September 2026;
that repository is retired and points here. Its history is reachable from this
repository's `main`.

## How it works

There is no GUI toolkit and no embedded browser engine. The binary:

- serves a `net/http` server bound to `127.0.0.1` and nothing else;
- serves a frontend of plain HTML/CSS/JS compiled in with `//go:embed` — no
  npm, no bundler, no build step;
- opens that URL in whatever browser the user already has installed.

The result is a single CGO-free executable that cross-compiles to all six
supported targets with nothing but `GOOS`/`GOARCH` and `go build`:
`windows/amd64`, `windows/arm64`, `linux/amd64`, `linux/arm64`,
`darwin/amd64`, `darwin/arm64`.

Dependencies: none. `go.mod` lists no third-party modules and there is no
`go.sum`, because everything used is in the standard library — including the
CLI dispatcher and the AUB REST client.

Neither AUE nor any map-building tool is imported as a Go library. Both are
separate programs reached through `os/exec`. That is an architectural boundary,
not an implementation detail: it is what keeps this repository MIT while AUE is
AGPL-3.0 and the map tools are GPL-2.0. See [THIRD_PARTY_NOTICES.md][notices].

## Install

No release artifacts are published yet. Build from source:

```console
$ go build -o companion ./cmd/companion
$ ./companion version
0.1.0-dev
```

Packaging skeletons for `.deb`/`.rpm` (nfpm), a Windows installer (Inno Setup)
and a macOS `.app` bundle live under [`build/`](build/) and are run by hand.
None of them is signed; there are no certificates yet.

```console
$ ./build/macos/make-app-bundle.sh --binary dist/darwin-arm64/companion \
    --arch arm64 --version 0.1.0 --out dist/darwin-arm64
```

## Configuration

Configuration lives in the OS-appropriate per-user config directory:

| Platform | Path |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/auto-pigeon-companion/config.json` (or `~/.config/…`) |
| macOS | `~/Library/Application Support/auto-pigeon-companion/config.json` |
| Windows | `%AppData%\auto-pigeon-companion\config.json` |

It is written 0600, because it holds an AUB session token.

```json
{
  "aub_base_url": "https://aub.example",
  "port": 8789,
  "tool_cache_dir": "",
  "game_roots": { "quake": "/games/quake" },
  "session": { "token": "…", "email": "you@example", "expires": "…" }
}
```

### AUB's address is configured, never compiled in

There is **no default AUB address**. The Companion does not guess where
auto-pigeon-backend lives; a wrong address that looks deliberate is worse than
a missing one that says so. Supply it with either:

- `AUCOM_AUB_BASE_URL` in the environment, which wins when both are set, or
- `"aub_base_url"` in `config.json`.

With neither, every AUB-backed operation reports that by name:

```console
$ companion auth status
aub: (not configured — set AUCOM_AUB_BASE_URL or aub_base_url)
signed in: no
```

### Environment variables

| Variable | Read by | Purpose |
| --- | --- | --- |
| `AUCOM_AUB_BASE_URL` | `internal/config` | where auto-pigeon-backend lives |
| `AUCOM_PASSWORD` | `companion auth login` | password for a scripted login |
| `AUCOM_AUE_BINARY` | `internal/aue` | an on-disk AUE to use instead of the embedded one |

`AUL_PASSWORD` and `AUC_AUE_BINARY`, the retired names, are still read.
`AUL_PASSWORD` warns when it is used.

### Upgrading from the Launcher, or from an older Companion

Migration is automatic and runs before every command; it writes nothing when
there is nothing to do. Run it explicitly to see what it did:

```console
$ companion migrate
migrated local configuration
  read     ~/.config/auto-pigeon-companion/config.json
  read     ~/.config/auto-pigeon-launcher/config.json
  backup   ~/.config/auto-pigeon-companion/config.json.pre-migration.bak
  carried  game root for quake from the Launcher config
  conflict aub_base_url: kept "https://aub.companion.example" from the Companion config, discarded "https://aub.launcher.example" from the Launcher config

$ companion migrate
configuration is already current; nothing to migrate
```

What it guarantees:

- the Launcher's own config file is **read, never written, moved or deleted**;
- the pre-migration Companion file is backed up before anything is overwritten;
- where both configs set a field, the Companion's wins and the discarded value
  is **reported**, never dropped silently;
- game roots merge per game, so both configs' games survive;
- a legacy session's token is carried over intact;
- a malformed config is refused rather than replaced;
- running it again changes nothing.

## Usage

Launching with **no subcommand** is GUI mode — it starts the server and opens
the browser. Named subcommands run headless.

```console
$ companion --help
companion — Auto-Pigeon Companion: build Quake maps, inspect them, and launch games

usage:
  companion                 start the local GUI and open a browser
  companion <command> [arguments]

commands:
  serve [--port <n>] [--open]                                             run the local GUI server without opening a browser
  auth login [--email <address>] | status | logout                        authenticate against auto-pigeon-backend
  build [--tool <name>] [--tool-version <v>] [-- <tool args>...]          run an external map-building tool
  profile validate | show | canonicalize | digest | diff | list | schema  read, check and compare tool, engine and pipeline profiles
  launch <game> [--map <name>] [--game-root <dir>] [--dry-run]            launch a game using its AUB launch config
  extractor version                                                       run the bundled auto-pigeon-extractor (AUE)
  migrate                                                                 fold Launcher and older Companion configuration into the current one
  version                                                                 print the build version
```

Exit codes: `0` success, `1` the operation failed, `2` the invocation was wrong.

### Serve

```console
$ companion serve --port 8791
companion 0.1.0-dev listening on http://127.0.0.1:8791/
```

The port is a preference, not a requirement: one already in use falls back to
an ephemeral one, so a stale instance cannot stop the app from starting. The
listener is `127.0.0.1` by construction — there is no flag that binds a
routable interface.

### Authenticate

```console
$ printf %s "$PASSWORD" | companion auth login --email you@example
signed in to https://aub.example as you@example

$ companion auth status
aub: https://aub.example
signed in: yes (you@example)
expires: 2026-09-20 21:14 (local estimate)

$ companion auth logout
signed out locally (the token stays valid at AUB until it expires)
```

`AUCOM_PASSWORD` is the alternative to a pipe. There is no terminal-echo
suppression: that needs cgo or `golang.org/x/term`, and both are excluded, so a
password typed at the prompt is visible and the prompt says so.

### Build

```console
$ companion build --tool noop
resolved noop@0.0.0-fake (linux/amd64)
verified ~/.cache/auto-pigeon-companion/tools/noop/0.0.0-fake/linux-amd64/noop
[noop] fake tool 0.0.0-fake
[noop] executable: ~/.cache/auto-pigeon-companion/tools/noop/0.0.0-fake/linux-amd64/noop
[noop] args:
[noop] no real map-building tool is wired up yet
[noop] done
```

`noop` is a fake tool, and it exists so the whole pipeline — resolve, cache,
SHA-256 verify, run, stream output — is exercised today. The cache layout and
the verification are real; a download that fails its checksum is discarded
rather than executed.

### Launch

```console
$ companion launch quake --map e1m1 --game-root /games/quake --dry-run
/games/quake/quakespasm -basedir /games/quake +map e1m1
```

Drop `--dry-run` to start it. With a game root in `config.json` the flag is
unnecessary.

### Extractor

```console
$ companion extractor version
auto-pigeon-extractor 0.4.1
```

A build with no embedded extractor and no `AUCOM_AUE_BINARY` reports that
instead:

```console
$ companion extractor version
error: no AUE binary is embedded in this build and AUCOM_AUE_BINARY is not set
```

## Profiles

A **profile** is a small JSON document that describes an external program the
Companion can drive: which programs it provides, what arguments they take, what
files they read and write, whether they go online, and how to get them. There
are three kinds.

| Kind | What it describes |
| --- | --- |
| `tool` | one program, or one family shipped together — a compiler, a visibility stage, a light stage |
| `engine` | a game engine, and the five things it can be asked to do |
| `pipeline` | an ordered list of *capabilities* and the files that flow between them |

The point of the format is that there is **one execution model**. The profiles
that ship inside the binary go through the same decoder, the same validation,
the same canonical encoding and the same resolution as a file you write by hand.
Adding a tool is a document, not a release — and the built-in ones cannot take a
shortcut that a user-authored one cannot, because there is no shortcut.

```console
$ companion profile list
engine   auto-pigeon.sample.q1-engine       1.0.0    builtin
         A worked example of the engine profile format, covering all five session actions.
pipeline auto-pigeon.sample.q1-normal       1.0.0    builtin
         Compile, compute visibility, compute lighting — the ordinary Quake 1 build.
tool     auto-pigeon.sample.q1-toolchain    1.0.0    builtin
         A worked example of the tool profile format: a three-stage Quake 1 map compile.
```

Those three are **samples**. They are complete, valid and exercised by the
tests, and they are not a qualified toolchain: the real EricW profiles and the
curated engine profiles arrive with the tasks that qualify them against upstream
releases.

### A profile is data, not a program

This is the whole security model, and it is worth stating as a list of things
the format **cannot express**:

- no shell string, anywhere — a command is an executable and an argument array,
  and the array is passed to the operating system directly;
- no script, hook, installer or `postinstall`;
- no embedded executable or library to load;
- no regular expression — output matching is by literal substring, because an
  untrusted regex is a denial-of-service primitive and no compiler diagnostic
  needs one;
- no absolute path, home directory, host address or credential — those describe
  one computer, and a profile is a file that travels;
- no free-text "extra arguments" box. Overrides are declared, typed options with
  ranges, because a free-text argument would make every other statement in the
  document decorative.

What it *can* express is an argument array written in a template language with
placeholders and nothing else:

```json
"args": [
  "-threads", "{option.threads}",
  { "value": "-nopercent", "when": { "option": "quiet" } },
  "{input.source_map}",
  "{output.bsp}"
]
```

Filesystem reach is declared by **role**, never by path, so the profile says
*what kind of place* it needs and your machine says where that is:

```json
"roots": [
  { "role": "workspace", "access": "read_write", "purpose": "read the staged map source and write the compiled BSP" }
]
```

### A complete, minimal profile

```json
{
  "schema_version": "aucom.profile/1.0",
  "kind": "tool",
  "id": "example.minimal",
  "version": "1.0.0",
  "name": "Minimal",
  "summary": "The smallest tool profile that is valid.",
  "publisher": { "name": "Example" },
  "license": { "spdx": "MIT" },
  "tool_version": "1.0.0",
  "platforms": [{ "platform": { "os": "linux", "arch": "amd64" }, "status": "supported" }],
  "acquisition": [{ "mode": "system_path", "title": "On PATH", "commands": ["example"] }],
  "executables": [{ "name": "main", "file": "example{platform.exe_suffix}" }],
  "actions": [{ "id": "run", "title": "Run it", "executable": "main", "args": ["--help"] }]
}
```

```console
$ companion profile validate minimal.tool.json
minimal.tool.json: valid tool profile example.minimal 1.0.0
  sha256:200c222ed007a86002f59cab9c5e210c9bd7952c2a390b7c8c7751567cba25f6
```

A document that is wrong is refused with every fault located, because the person
repairing it wants the list and not the first item on it:

```console
$ companion profile validate broken.tool.json
broken.tool.json is not a valid profile:
  actions[0].args[0].value: contains "$(", which is command substitution — commands are an executable and an argument array; there is no shell, so write the value literally
$ echo $?
1
```

Exit codes follow the rest of the CLI: `0` valid, `1` the document is not, `2`
the command was typed wrong.

### Trust, and why importing is inert

Every profile is in one of four states, and the state answers exactly one
question: *who vouches for this?*

| State | Who vouches |
| --- | --- |
| `builtin` | it arrived with the program you installed |
| `verified` | an Auto-Pigeon catalogue signature covers these exact bytes |
| `community` | nobody |
| `local` | nobody |

`local` is not the friendly state. "Local" describes where a file *is*, and a
file's location is the easiest thing in the system for something else to
arrange — an installer, a sync client, an extracted archive. So `local` and
`community` are treated the same: both need your approval, recorded against the
document's digest.

Importing does nothing on its own. A profile can be read, checked,
canonicalized, digested and displayed before you have decided anything, and none
of that fetches or runs a thing:

```console
$ companion profile show community-toolchain.tool.json
Andrea's Q1 compile 0.3.0 (example.andrea.q1-compile)
  published by A Companion user, under GPL-2.0-or-later
  Community — imported from elsewhere; nobody has checked it for you.

  If you approve it, it may:
    - Run Andrea's Q1 compile (qbsp) as a program on your computer. [high]
    - Read files in a scratch folder created for this job. [low]
    - Create and change files in a scratch folder created for this job. [low]

  digest: sha256:27ef7df7c8bb5bcc596824868f6eafedd3a08f885664bbf444d946d3ab8fe5c9

  Nothing here has been granted. Importing a profile does not let it do any of the above.
```

Approval is recorded against the **digest**, not the version number, so an
update that asks for more is refused until you have seen what changed:

```console
$ companion profile diff installed.tool.json incoming.tool.json
Changes (5):
  + actions[0].network = {"hosts":["updates.example.com"],"purpose":"check for tool updates","required":true}
  + actions[0].roots[1] = {"access":"read_write","purpose":"copy the finished map into the game folder","role":"game_root"}
  ~ description: "This document exists to be the other half of a test. It is written the way a person writ…" -> "An update that adds a network permission and write access to the installed game folder. N…"
  ~ summary: "A hand-written tool profile that drives the same three programs as the built-in sample." -> "The same profile, one version later, asking for two things it did not ask for before."
  ~ version: "0.3.0" -> "0.4.0"

It now asks to:
  - Create and change files in your installed game folder. [high]
  - Connect to updates.example.com. [medium]
  - Read files in your installed game folder. [medium]

This update asks for more than the installed version did, so it needs your decision again.
```

Editing a profile drops it to `local`, whatever it was before, because a
signature covers bytes and changing the bytes does not produce a
differently-signed document. Nothing is ever promoted to `builtin`: that is a
fact about the release, not a status a file can earn.

### Where the profile stops and your machine starts

A profile says *what kind of place* it needs. Where those places are on your
computer is a **local binding** — absolute paths, the version the tool reported
when it was last asked, the AUB record the Game Profile slug resolves to on your
account, and what you approved. A binding is never published, and it is a
different type in a different package for exactly that reason
([ADR-0002](docs/adr/0002-portable-profiles-and-local-bindings-are-different-types.md)).

The same rule as `.env` addresses, applied to documents: **a file that travels
must not carry a location.** A profile containing `/home/you/quake` would be
wrong on every other machine, and a profile containing `127.0.0.1` would name
the reader's own computer. Both are refused, by name:

```console
$ companion profile validate leaky.tool.json
leaky.tool.json is not a valid profile:
  actions[0].roots[0].purpose: contains the absolute path "/home/andrea/quake/id1/maps", which is a path on one machine and wrong on every other — name a root role — workspace, project_root, game_root, content_root, tool_root, tool_cache — and let the local binding say where it is on this machine
```

### Game Profiles belong to AUB

A profile does **not** describe what a game is. Which engine family a project
uses, its map dialect, its texture model and its entity vocabulary are a *Game
Profile* — a document AUB owns and the AUP editor already reads. Profiles here
reference one by **slug**, and stop:

```json
"game_profile": { "slug": "quake1", "engine_family": "quake1" }
```

By slug rather than by AUB record id, because a record id identifies a row in
one AUB deployment and would mean nothing — or something else — anywhere the
document travelled. Resolving the slug against your account is a binding's job.

### The published schemas

Four JSON Schema (2020-12) documents are embedded in the binary and are the
contract for anything outside the Companion — an editor, a CI check, a second
implementation:

```console
$ companion profile schema
engine-profile-1.0.schema.json
local-binding-1.0.schema.json
pipeline-profile-1.0.schema.json
profile-common-1.0.schema.json
tool-profile-1.0.schema.json

$ companion profile schema tool-profile-1.0.schema.json > tool.schema.json
```

The Go types in `internal/profile` are the enforcement point — they check things
a schema cannot express, such as whether a placeholder names a declared input.
A test derives each type's member set by reflection and asserts the schema lists
exactly those members with exactly the same ones required, so the two
descriptions cannot drift.

### Versions, compatibility and migration

Three version numbers do three different jobs, and confusing them is the usual
forward-compatibility bug:

| Field | Changes when |
| --- | --- |
| `schema_version` | the *document format* changes — currently `aucom.profile/1.0` |
| `version` | the *profile document* changes; immutable once published |
| `tool_version` / `engine_version` | the *upstream program* changes |

The policy:

- **A published version is immutable.** Changing what `1.2.0` says, rather than
  publishing `1.2.1`, is how a reviewed and approved profile silently becomes a
  different program — and the version number, which is what a catalogue, a
  changelog and a person all point at, would then mean two things. Canonical
  encoding and digests make it detectable; a grant recorded against a digest
  makes it refused; and a diff says so in words rather than showing it as an
  ordinary update:

  ```console
  $ companion profile diff installed.tool.json republished.tool.json
  This document has the same id and version as the one installed, and says something different. A published version is immutable: whatever changed should have been a new version.
  …
  ```
- **A document naming an unknown `schema_version` is refused by name**, never
  read hopefully. A format this build has never seen is exactly the case where a
  partial read is worse than no read:

  ```console
  $ companion profile validate from-the-future.tool.json
  from-the-future.tool.json is not a valid profile:
    schema_version: is "aucom.profile/9.9", which this build of the Companion cannot read — this build reads: aucom.profile/1.0
  ```

- **Unknown members are refused, with their path.** Ignoring one silently is how
  a `network` member a reviewer read stops being enforced.
- **A new format version is a new schema file and a new entry in the supported
  list.** A build reads every version it lists; documents are not rewritten on
  disk, and a profile's own `compatibility.companion` range is what an author
  uses to say which Companion versions a document was written for.
- **The local binding format is versioned separately** (`aucom.local-binding/1.0`)
  because local state and published documents have different compatibility
  obligations. Tying them together would force a migration of your settings
  every time the published format moved.

### Profiles configure independent programs; they do not relicense them

A profile is configuration for software the Auto-Pigeon project did not write
and does not distribute. Describing a program is not distributing it.

- The map-building tools and game engines these profiles drive are **separate
  programs under their own licences**, usually GPL-2.0. They are run as separate
  operating-system processes, exactly as `internal/tools` requires — never
  linked, never vendored, never compiled in.
- **This repository's MIT licence covers this repository's own code.** It does
  not extend to a tool a profile describes, and it is not extended by one. A
  profile's `license` block states the described program's licence, carries its
  notice, and carries the corresponding-source link a copyleft licence needs
  when a binary is offered for download — so the acquisition path can show all
  of it before anything is fetched.
- **Approving a profile is not a licence grant and does not change your
  obligations** under the described program's licence. It is your decision to
  let this program run that one.
- Commercial game data — PAK files, maps and textures that came with a game you
  bought — is not covered by any of those licences and is never copied, uploaded
  or redistributed by the Companion.

See [THIRD_PARTY_NOTICES.md][notices] for what is compiled in, what is run as a
separate process, and what a release redistributes.

## HTTP API

The server is loopback-only and same-origin guarded: a request carrying a
cross-origin `Origin` header is refused, and every mutating route is POST-only.
A request with no `Origin` — a plain navigation, or the `curl` calls below — is
allowed.

```console
$ companion serve --port 8791 &

$ curl -s http://127.0.0.1:8791/api/status
{"version":"0.1.0-dev","aub_base_url":"https://aub.example","authenticated":true,
 "email":"you@example","platform":"linux/amd64",
 "tool_cache_dir":"/home/you/.cache/auto-pigeon-companion/tools","aue_available":false}

$ curl -s http://127.0.0.1:8791/api/launch-configs
{"items":[{"game":"quake","executable_pattern":"{game_root}/quakespasm{exe}",
 "args":["-basedir","{game_root}","+map","{map}"]}, …]}

$ curl -s -X POST http://127.0.0.1:8791/api/auth/login \
    -d '{"email":"you@example","password":"…"}'
{"email":"you@example"}

$ curl -s -X POST http://127.0.0.1:8791/api/build -d '{"tool":"noop"}'
{"output":"resolved noop@0.0.0-fake (linux/amd64)\n…"}

$ curl -s -X POST http://127.0.0.1:8791/api/launch \
    -d '{"game":"quake","map":"e1m1","dry_run":true}'
{"command":"/games/quake/quakespasm -basedir /games/quake +map e1m1",
 "plan":{"executable":"/games/quake/quakespasm","args":[…],"working_dir":"/games/quake"},
 "started":false}

$ curl -s http://127.0.0.1:8791/api/aue/version
{"version":"auto-pigeon-extractor 0.4.1"}

$ curl -s -o /dev/null -w '%{http_code}\n' \
    -H 'Origin: http://evil.example' http://127.0.0.1:8791/api/status
403

$ curl -s -X POST http://127.0.0.1:8791/api/auth/logout
{"authenticated":false}
```

| Route | Method | Purpose |
| --- | --- | --- |
| `/` | GET | the embedded frontend |
| `/api/status` | GET | version, platform, AUB address, sign-in state, extractor availability |
| `/api/auth/login` | POST | sign in and persist the session |
| `/api/auth/logout` | POST | forget the session locally |
| `/api/launch-configs` | GET | available launch configurations |
| `/api/build` | POST | run one external tool and return its output |
| `/api/launch` | POST | resolve a launch plan, and run it unless `dry_run` |
| `/api/aue/version` | GET | the bundled extractor's version |

A local server has no per-request authentication: any process running as the
same user can drive it while it is up. A startup token in the opened URL is the
usual remedy and is noted in `internal/web` as work to do before any release.

## Development

```console
$ gofmt -l .
$ go vet ./...
$ go test ./...
$ GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/companion
```

CI runs the tests on Linux, Windows and macOS, checks `gofmt`, and
cross-compiles all six targets. It publishes no releases.

A development build embeds no extractor. Point at a locally built one instead:

```console
$ AUCOM_AUE_BINARY=../auto-pigeon-extractor/bin/auto-pigeon-extractor \
    ./companion extractor version
```

## Licence

MIT — see [LICENSE](LICENSE). That covers **this repository's own code only**.

AUE is AGPL-3.0 and the external map-building tools are GPL-2.0; both are
separate programs, and neither is relicensed by anything here.
[THIRD_PARTY_NOTICES.md][notices] sets out what is compiled in, what is run as
a separate process, and what a release redistributes — including the open
decision about embedding AUE.

[aub]: https://github.com/andrea-dintino/auto-pigeon-backend
[aue]: https://github.com/andrea-dintino/auto-pigeon-extractor
[notices]: THIRD_PARTY_NOTICES.md
