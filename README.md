# Auto-Pigeon Companion

The local Auto-Pigeon runtime: one MIT-licensed desktop application that signs
in against **AUB** ([auto-pigeon-backend][aub], a PocketBase instance), builds
Quake maps with external map-building tools, inspects them by driving **AUE**
([auto-pigeon-extractor][aue]) as a subprocess, and launches games.

It runs as a local web app in the browser you already have, or headless from
the command line. Both surfaces are the same binary.

> **Status: the runtime is real; the tools are not chosen yet.** The profile
> model, the job executor, the local API and the CLI are implemented and
> tested — a profile action becomes a supervised process with a workspace,
> bounded logs, collected artifacts and a record that survives a crash. What is
> still missing is upstream: no external map-building toolchain has been
> qualified, so there is nothing to acquire automatically, and launch
> configurations come from a local stub because AUB's schema is not confirmed.
> Point `--executable` at a copy you already have and the whole path works
> today. Every placeholder is marked in the source at the point it will be
> replaced.

This repository absorbed **auto-pigeon-launcher** (AUL) in September 2026;
that repository is retired and points here. Its history is reachable from this
repository's `main`.

## How it works

There is no GUI toolkit and no embedded browser engine. The binary:

- serves a `net/http` server bound to `127.0.0.1` and nothing else;
- serves a frontend of plain HTML/CSS/JS compiled in with `//go:embed` — no
  npm, no bundler, no build step;
- opens that URL in whatever browser the user already has installed;
- runs every external program through one supervised job runtime, which the
  page and the command line both drive — see [Jobs](#jobs).

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
  "jobs_dir": "",
  "profiles_dir": "",
  "job_concurrency": 0,
  "game_roots": { "quake": "/games/quake" },
  "session": { "token": "…", "email": "you@example", "expires": "…" }
}
```

Three more files sit beside it, and each is somewhere different for a reason:

| What | Where | Why there |
| --- | --- | --- |
| `bindings.json` | the config directory | it records what you installed and what you approved — a cache clean must not silently withdraw a grant |
| `profiles/` | the config directory | an imported profile is something you chose and reviewed |
| `jobs/` | the *cache* directory | job records, logs and published artifacts are reproducible; clearing caches loses build history, not work |
| `api-token` | the config directory, while a server runs | mode 0600, deleted on shutdown — see [HTTP API](#http-api) |

`jobs_dir` and `profiles_dir` override the first two paths, as do
`AUCOM_JOBS_DIR` and `AUCOM_PROFILES_DIR`. `job_concurrency` is how many jobs
run at once; `0` lets the executor pick from the machine.

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
| `AUCOM_JOBS_DIR` | `internal/config` | where job records, logs and artifacts live |
| `AUCOM_PROFILES_DIR` | `internal/config` | where imported profile documents are read from |

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
  serve [--port <n>] [--open]                                                     run the local GUI server without opening a browser
  auth login [--email <address>] | status | logout                                authenticate against auto-pigeon-backend
  job run | preview | list | show | logs | cancel | retry | artifacts | profiles  run a profile action as a supervised job, and inspect what ran
  profile validate | show | canonicalize | digest | diff | list | schema          read, check and compare tool, engine and pipeline profiles
  launch <game> [--map <name>] [--game-root <dir>] [--dry-run]                    launch a game as a supervised job, using its AUB launch config
  extractor version                                                               run the bundled auto-pigeon-extractor (AUE)
  migrate                                                                         fold Launcher and older Companion configuration into the current one
  version                                                                         print the build version
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

### Jobs

Every program the Companion runs is a **job**. A compile, a game, a version
probe: same queue, same supervision, same record afterwards. There is one
executor and nothing goes round it.

`companion job profiles` is what can be run on this machine — the documents
compiled into the build, plus any you have imported, plus an engine profile
generated from each of your launch configs:

```console
$ companion job profiles
auto-pigeon.sample.q1-engine             engine  builtin   play_map, play_package, join_server, host_listen, host_dedicated
auto-pigeon.sample.q1-normal             pipeline builtin
auto-pigeon.sample.q1-toolchain          tool    builtin   compile, vis, light
auto-pigeon.launch.quake                 engine  builtin   play_map
auto-pigeon.launch.quake2                engine  builtin   play_map
```

**Preview first.** `job preview` resolves an action into the exact command and
starts nothing. It is the same resolution the run does, against the same
workspace layout, so what you approve is what starts — the argument array is
printed one element per line, because where each argument begins and ends is the
whole point of not having a shell:

```console
$ companion job preview \
    --profile auto-pigeon.sample.q1-toolchain --action compile \
    --executable qbsp=/usr/local/bin/qbsp \
    --input source_map=./level.map --option basename=level
profile:  auto-pigeon.sample.q1-toolchain 1.0.0 (builtin)
action:   compile
digest:   sha256:7b2d92f7352a56090602c3ce41621198884c60e7a51adb7d14416e8cf37f3d27
workdir:  ~/.cache/auto-pigeon-companion/jobs/20260906T235142Z-ab2db2e1f694/workspace
command:  /usr/local/bin/qbsp -threads 4 …/workspace/input/source_map/level.map …/workspace/compile/level.bsp
argv:
  [0] /usr/local/bin/qbsp
  [1] -threads
  [2] 4
  [3] …/jobs/20260906T235142Z-ab2db2e1f694/workspace/input/source_map/level.map
  [4] …/jobs/20260906T235142Z-ab2db2e1f694/workspace/compile/level.bsp
environment:
  HOME=…/jobs/20260906T235142Z-ab2db2e1f694/home
  LANG=C
  LC_ALL=C
  PWD=…/jobs/20260906T235142Z-ab2db2e1f694/workspace
  TEMP=…/jobs/20260906T235142Z-ab2db2e1f694/tmp
  TMP=…/jobs/20260906T235142Z-ab2db2e1f694/tmp
  TMPDIR=…/jobs/20260906T235142Z-ab2db2e1f694/tmp
  USERPROFILE=…/jobs/20260906T235142Z-ab2db2e1f694/home
```

**Then run it.** The program's output reaches your terminal as it is produced
*and* goes into the job's bounded log; that is one execution with two readers,
not a streaming mode beside a recording one.

```console
$ companion job run \
    --profile auto-pigeon.sample.q1-engine --action play_map \
    --executable engine=/bin/echo --root game_root=/games/quake \
    --root content_root=/games/quake --runtime map_name=e1m1
job 20260906T235206Z-6d2022e01cd7 queued
-basedir /games/quake +map e1m1
job 20260906T235206Z-6d2022e01cd7: succeeded — finished, and every required output was produced
  auto-pigeon.sample.q1-engine play_map, 1ms
  exit status 0
```

(`/bin/echo` stands in for a real engine above, so the example runs on a machine
with no game installed and prints the argument array the engine would have got.)

**Everything a job did is still there afterwards.**

```console
$ companion job list
20260906T235206Z-6d2022e01cd7  succeeded    1ms        auto-pigeon.sample.q1-engine play_map

$ companion job logs 20260906T235206Z-6d2022e01cd7
-basedir /games/quake +map e1m1

$ companion job artifacts 20260906T235206Z-6d2022e01cd7
no artifacts
```

`job cancel <id>` stops a job — including one the GUI started, because a stop is
a marker in the job's own directory and whichever process owns the job notices
it within a second. `job retry <id>` runs a finished job's request again as a
**new** job, which is the only way anything here re-runs: the record of what
happened the first time is never overwritten, and an interrupted job — one whose
outcome nobody knows — is never repeated without being asked.

Exit status: `0` the job succeeded, `1` it did not, `2` the command was typed
wrong. `--json` prints the whole record for a script.

#### What the executor guarantees

- **There is no shell.** A command is an executable path and a `[]string`, all
  the way down to `execve`. An argument containing `; rm -rf ~` is one argument
  whose value is `; rm -rf ~`. There is no parser between the document and the
  kernel for a payload to be interesting to.
- **Nothing is inherited.** The environment is constructed, not passed through:
  `HOME` and the temporary directory point inside the job's own workspace, the
  locale is fixed so a tool's output does not change with your settings, and
  what a profile asked to inherit is inherited by name and nothing else. `PATH`
  is absent, and a profile that asks for it is refused.
- **Inputs are copied in, outputs are copied out.** Your source file is read
  once at staging time and never written to, which is why cleaning up a job
  cannot lose it. Containment is rechecked *after* the tool has run, because a
  file can become a symlink between resolution and collection.
- **Output is bounded but never blocked.** Both streams are drained
  continuously; what is *kept* is the first and last 256 KiB with a count of
  what fell out of the middle. A noisy compiler cannot exhaust memory, and the
  Companion is never the reason a build stops making progress.
- **Two logs.** The raw one is the exact bytes the program wrote, invalid UTF-8
  and all — that is the evidence. The user view is what you read: valid UTF-8,
  no terminal control sequences, credentials redacted. `--raw` asks for the
  first.
- **A crash is admitted, not repaired.** A job whose supervisor went away comes
  back as `interrupted`, which means *nobody knows how this ended*. Running it
  again is `job retry`, and it is your decision.

It is not a sandbox, and the Companion will not pretend otherwise: nothing here
can stop a program you authorised from writing wherever you can write. What the
containment checks stop is a *document* directing a program outside its declared
roots, and the Companion reading or publishing anything outside them.

### Launch

```console
$ companion launch quake --map e1m1 --game-root /games/quake --dry-run
/games/quake/quakespasm -basedir /games/quake +map e1m1
```

Drop `--dry-run` to start it. With a game root in `config.json` the flag is
unnecessary.

Starting a game is a job. The launch config becomes a generated engine profile
— `auto-pigeon.launch.quake` in `companion job profiles` above — and the start
is a submission to the same executor a compile goes through, so a game you
launched is listed, cancellable and recorded like anything else. `companion
launch` is the short way to say it; `companion job run --profile
auto-pigeon.launch.quake --action play_map --runtime map_name=e1m1` is the same
thing spelled out.

The generated profile is a bridge, not the model: it has one action, no content
layouts and no version probe, because a stubbed launch config does not know
enough to claim more. A curated engine profile replaces it by existing — the
catalog prefers a document to a generated stand-in.

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

The server is loopback-only, and four checks stand between it and anything that
is not you. It can start processes on your machine; loopback alone is not a
boundary, because a page you have open on some other origin can script requests
at a guessable local port.

1. **A token, in a header.** Minted per run, published beside `config.json` at
   mode 0600, removed when the server stops. It is a header rather than a cookie
   on purpose: a cross-origin page cannot set a custom header without a CORS
   preflight, this server answers none, and nothing is ever authenticated by
   something the browser attaches on its own. There is no CSRF surface because
   there is no ambient credential.
2. **`Host` must name a loopback address.** That is the DNS-rebinding defence:
   `http://rebound.example/` resolving to `127.0.0.1` still arrives with
   `Host: rebound.example`, and is refused before anything reads it.
3. **`Origin`, when present, must match `Host`.**
4. **`Sec-Fetch-Site`, when the browser sends it,** must say the request came
   from this origin or from no page at all.

Requests with no `Origin` — a plain navigation, or the `curl` calls below — are
allowed, so scripting works.

```console
$ companion serve --port 8791 &
companion 0.1.0-dev listening on http://127.0.0.1:8791/
API token written to ~/.config/auto-pigeon-companion/api-token

$ TOKEN=$(cat ~/.config/auto-pigeon-companion/api-token)

$ curl -s -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/status
{"version":"0.1.0-dev","aub_base_url":"","authenticated":false,
 "platform":"linux/amd64","tool_cache_dir":"/home/you/.cache/auto-pigeon-companion/tools",
 "jobs_dir":"/home/you/.cache/auto-pigeon-companion/jobs","aue_available":false}

$ curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8791/api/status
401

$ curl -s -o /dev/null -w '%{http_code}\n' -H "X-AUCOM-Token: $TOKEN" \
    -H 'Origin: http://evil.example' http://127.0.0.1:8791/api/status
403

$ curl -s -o /dev/null -w '%{http_code}\n' -H "X-AUCOM-Token: $TOKEN" \
    -H 'Host: rebound.example' http://127.0.0.1:8791/api/status
403

$ curl -s -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/profiles | head -c 120
{"items":[{"actions":[{"capability":"q1.bsp.compile","id":"compile", …

$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/jobs \
    -d '{"profile":"auto-pigeon.sample.q1-engine","action":"play_map",
         "executables":{"engine":"/bin/echo"},
         "roots":{"game_root":"/games/quake","content_root":"/games/quake"},
         "runtime":{"map_name":"e1m1"}}'
{"schema_version":"aucom.job/1.0","id":"20260906T235240Z-a8b4d547bf25","state":"queued", …

$ curl -s -H "X-AUCOM-Token: $TOKEN" \
    http://127.0.0.1:8791/api/v1/jobs/20260906T235240Z-a8b4d547bf25 | head -c 80
{"schema_version":"aucom.job/1.0","id":"20260906T235240Z-a8b4d547bf25","state":"succeeded", …

$ curl -s -H "X-AUCOM-Token: $TOKEN" \
    'http://127.0.0.1:8791/api/v1/jobs/20260906T235240Z-a8b4d547bf25/logs'
{"job":"20260906T235240Z-a8b4d547bf25","stream":"stdout","raw":false,
 "summary":{"bytes":38,"stored":38,"dropped":0,"lines":1,"truncated":false,"file":"stdout.log"},
 "text":"-basedir /games/quake +map e1m1\n"}

$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/auth/logout
{"authenticated":false}
```

| Route | Method | Purpose |
| --- | --- | --- |
| `/` | GET | the embedded frontend, carrying this run's API token |
| `/api/status` | GET | version, platform, AUB address, sign-in state, extractor availability |
| `/api/auth/login` | POST | sign in and persist the session |
| `/api/auth/logout` | POST | forget the session locally |
| `/api/launch-configs` | GET | available launch configurations |
| `/api/launch` | POST | resolve a launch, and submit it as a job unless `dry_run` |
| `/api/aue/version` | GET | the bundled extractor's version |
| `/api/v1/jobs` | GET, POST | list jobs; submit one |
| `/api/v1/jobs/preview` | POST | resolve a request into its exact command, start nothing |
| `/api/v1/jobs/{id}` | GET | one job's whole record |
| `/api/v1/jobs/{id}/cancel` | POST | ask it to stop |
| `/api/v1/jobs/{id}/retry` | POST | run its request again, as a new job |
| `/api/v1/jobs/{id}/logs` | GET | `?stream=stdout\|stderr`, `?raw=1` for the bytes the program wrote |
| `/api/v1/jobs/{id}/artifacts` | GET | what it produced |
| `/api/v1/jobs/{id}/artifacts/{name}` | GET | download one |
| `/api/v1/profiles` | GET | what can be run on this machine |
| `/api/v1/profiles/{id}` | GET | one profile, with its permissions |
| `/api/v1/profiles/validate` | POST | check a document without importing it |

The `/api/v1` routes are versioned because `companion job` and your own scripts
drive them; the unversioned `/api` routes are the page's own and are not a
contract.

**What the token does not cover, said plainly.** The page has to be able to
load, so `GET /` is unauthenticated and carries the token — which means a
process running as you can read it. That process can already read `config.json`,
which holds your AUB session token, so this is the boundary that was there
anyway. What the token closes is the case that boundary never covered: a web
page, on some other origin, driving the executor.

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
