# Auto-Pigeon Companion

The local Auto-Pigeon runtime: one MIT-licensed desktop application that signs
in against **AUB** ([auto-pigeon-backend][aub], a PocketBase instance), builds
Quake maps with external map-building tools, inspects them by downloading,
verifying and driving **AUE** ([auto-pigeon-extractor][aue]) as a separate
process, and launches games.

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
>
> The seven Quake engine profiles that ship are curated from each engine's own
> published command line, and every platform in them is marked `unverified`
> because no build of any of them has been run by this project. That claim is
> in the documents, not only in this paragraph.

This repository absorbed **auto-pigeon-launcher** (AUL) in September 2026;
that repository is retired and points here. Its history is reachable from this
repository's `main`.

## How it works

There is no GUI toolkit and no embedded browser engine. The binary:

- serves a `net/http` server bound to `127.0.0.1` and nothing else;
- serves a frontend of plain HTML/CSS/JS compiled in with `//go:embed` — no
  npm, no bundler, no build step;
- opens that URL in whatever browser the user already has installed;
- obtains those programs by one of four declared routes, of which the only one
  that downloads anything goes through a signed, revocable catalogue — see
  [Acquiring tools](#acquiring-tools);
- runs every external program through one supervised job runtime, which the
  page and the command line both drive — see [Jobs](#jobs);
- describes the Quake engines it can start as documents rather than as code, so
  adding one is a file and not a release — see [Engines](#engines);
- writes the PAK and PK3 archives itself, natively, because that last step is
  where a build stops being reproducible and where somebody else's content
  accidentally gets published — see [Packaging](#packaging-pak-and-pk3).

The result is a single CGO-free executable that cross-compiles to all six
supported targets with nothing but `GOOS`/`GOARCH` and `go build`:
`windows/amd64`, `windows/arm64`, `linux/amd64`, `linux/arm64`,
`darwin/amd64`, `darwin/arm64`.

Dependencies: none. `go.mod` lists no third-party modules and there is no
`go.sum`, because everything used is in the standard library — including the
CLI dispatcher and the AUB REST client.

Neither AUE nor any map-building tool is imported as a Go library, and neither
is compiled into this binary. Both are separate programs, obtained separately
against a signed catalogue and reached through `os/exec`. That is an
architectural boundary, not an implementation detail: it is what keeps this
repository MIT while AUE is AGPL-3.0 and the map tools are GPL-2.0. See
[THIRD_PARTY_NOTICES.md][notices].

## Install

No release artifacts are published yet. Build from source:

```console
$ go build -o companion ./cmd/companion
$ ./companion version
0.1.0-dev
```

That builds the Companion and nothing else. **The extractor is not part of it**
and is obtained separately — see [Extractor](#extractor).

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

It is written 0600, because it holds an AUB session token. That token is the
only credential this program stores; it is never written to the asset cache, to
a job record, to a build manifest or to any log line, and the GUI's own
`api-token` is a separate loopback-only secret deleted on shutdown.

```json
{
  "aub_base_url": "https://aub.example",
  "port": 8789,
  "tool_cache_dir": "",
  "jobs_dir": "",
  "profiles_dir": "",
  "asset_cache_dir": "",
  "catalog_url": "https://catalog.example/auto-pigeon/",
  "catalog_anchors_path": "/etc/auto-pigeon/anchors.json",
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
| `catalog-state.json` | the config directory | the highest catalogue serial accepted and every revocation ever seen — see [Acquiring tools](#acquiring-tools) |
| `license-acceptance.json` | the config directory | which licence notices you have been shown |
| `tools/` | the *cache* directory | downloaded packages, re-fetchable by definition |
| `assets/` | the *cache* directory | verified copies of AUB assets — re-fetchable, and a build that pinned one names it in its manifest |

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
| `AUCOM_AUE_BINARY` | `internal/aue` | an on-disk AUE to use instead of the verified one — a local, **unverified** development override |
| `AUCOM_JOBS_DIR` | `internal/config` | where job records, logs and artifacts live |
| `AUCOM_PROFILES_DIR` | `internal/config` | where imported profile documents are read from |
| `AUCOM_ASSET_CACHE_DIR` | `internal/config` | where assets synced from AUB are cached |
| `AUCOM_CATALOG_URL` | `internal/config` | where the signed acquisition catalogue is fetched from |
| `AUCOM_CATALOG_ANCHORS` | `internal/config` | the file holding the catalogue's trust anchors |
| `AUCOM_OFFLINE` | `internal/config` | forbid every network access; installed packages stay usable |

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

## The desktop interface

Running the binary with no arguments starts the local server and opens your
browser at it. There is no window toolkit anywhere in this program: the window
is your own browser, which is what keeps the binary CGO-free and buildable for
all six targets with plain `go build`. Everything the page can do, the CLI can
do too, through the same services — there is no browser-only path into anything.

The page has six areas, and the order is the order of a first run.

| Area | What it is for |
| --- | --- |
| **Library** | Your maps and assets on auto-pigeon-backend, and the exact revisions this machine has downloaded and verified. |
| **Build** | Compiling: which pipeline, which tool provides each stage, the exact command, live output, artifacts. |
| **Run** | Starting a game: which engine, where it is on this machine, which map or package, and the exact command. |
| **Profiles** | Reading what a tool or engine asks to be allowed to do, approving it, and writing your own. |
| **Jobs** | Everything that has run, with its command, its exit status and its output. |
| **Settings** | Where the backend is, and where the Companion keeps things. |

### The default path through it

Sign in → choose an exact map revision in **Library** and download it → in
**Build**, pick a pipeline and press Build → in **Run**, pick the engine you
have and start it. Expert controls — a step's options, the raw profile document,
a hand-written engine — are all reachable and none of them are on that path.

### Two halves of every profile, kept apart

A profile is portable: it describes a program, and it contains no path on
anybody's machine. What it does *not* contain is where that program is on
**your** machine, which directories it may read, and whether you have approved
it. That half is a **local binding**, and the interface never mixes the two —
the profile panel shows them as separate sections, and exporting a profile
carries none of your paths with it.

Every profile carries a trust state, shown as a badge with a word in it rather
than as a colour:

| Badge | What it means |
| --- | --- |
| `builtin` | Shipped inside this build of the Companion. Trusting it is the same act as trusting the program. |
| `verified` | Signed by an Auto-Pigeon catalogue key, covering this document's exact digest. |
| `community` | Came from somewhere else. It may be excellent; nothing here knows. |
| `local` | Written or edited on this machine, or found in your profile folder. Not a synonym for safe. |

Only a `builtin` profile runs without your approval, because it arrived inside
the program you installed. **A `verified` one still has to be approved**: a
signature says who published a document, never that you agreed to what it asks
for. Everything else — verified, community, local — cannot run until you have
read what it asks for and approved it, against that exact document's digest.
Importing grants nothing, and a document that changes after you approved it
needs approving again. This is `profile.Authorize` in `internal/profile`, not a
rule the page implements: the API has no route that skips it, and what the page
shows is that function's own answer.

### The profile wizard

**Profiles → New profile** starts from a profile that has been tested, changes
what is different about yours, and shows you the result. Raw JSON is not the
normal path: four steps of ordinary form fields are, and the JSON view is behind
a disclosure at the end for people who want it.

The document is composed and validated by the Companion, never by the page — so
what the advanced view shows is the real document, canonically encoded, with the
digest it will actually have. Beside it is a **normalized diff against the
template**, which is how you see what you changed rather than what you meant to.

That view is also the import and export path: paste a profile somebody sent you,
press *Check this document*, read what it asks for, and install it. Setting up an
engine nobody anticipated needs no change to this program's source.

### Choosing files without giving the browser your disk

A browser cannot hand a page a *path*, and it certainly cannot hand it a
directory. So the Companion opens the desktop's own file chooser — `zenity` or
`kdialog` on Linux and the BSDs, `osascript` on macOS, PowerShell's dialogs on
Windows — and the one path you picked comes back.

There is no directory listing, no `stat` and no completion in the API. The page
is never given the filesystem; it is given the answer to one question a person
answered in a dialog they saw.

On a machine with no chooser installed — a headless server, a minimal container
— the **Browse** button is not drawn at all, and the text field beside it is the
whole answer. A typed path goes through exactly the same validation the dialog's
answer does: absolute, no control characters, and of the kind that was asked
for, with the reason stated when it is not.

### Nothing important is only a message

A message that appears and disappears is not evidence. Every download, build,
launch and approval leaves a record that is read back from disk — the cached
revision list, the build manifest, the job, the binding file — and the page
shows those, not a memory of what it was told. Reloading the page, or restarting
the Companion, loses nothing and re-runs nothing.

The Jobs area additionally keeps *Recent actions in this window*: a list of what
this window asked for and what it was told. It is a convenience, every line
points at something durable, and clearing it deletes nothing.

### Keyboard, screen readers, and small windows

Every control is a native `button`, `input` or `select`, so all of them are in
the tab order. The first thing focus reaches is a **Skip to the main content**
link. Switching area moves focus to the new heading, so a keyboard or
screen-reader user lands on the content that changed rather than being left
behind it. Status changes are announced through a polite live region; errors
carry the word *Error* as well as a colour.

The whole first-run journey — sign in, download, approve, build, launch — is
tested in a real headless browser at 1280 pixels and at the narrowest window
Chrome will open, and neither one scrolls sideways. Animation is confined to one
spinner, which stops moving under `prefers-reduced-motion` and always has text
beside it saying what is happening.

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
  serve [--port <n>] [--open]                                                              run the local GUI server without opening a browser
  auth login [--email <address>] | status | logout                                         authenticate against auto-pigeon-backend
  aub capabilities | catalog | show | revisions | sync | cached | verify | export | clean  browse auto-pigeon-backend's assets and sync exact revisions to this machine
  job run | preview | list | show | logs | cancel | retry | artifacts | profiles           run a profile action as a supervised job, and inspect what ran
  build run | preview | list | show | pipelines                                            build a map through a pipeline: several supervised jobs, wired, with a manifest
  package targets | preview | create | inspect | verify | extract                          build a PAK or PK3 from what a build produced, and read one somebody else made
  profile validate | show | canonicalize | digest | diff | list | schema                   read, check and compare tool, engine and pipeline profiles
  acquire plan | install | accept | list | verify | use | gc | resolve                     obtain a profile's programs from the signed catalogue, and manage the cache
  catalog keygen | sign | verify | show | status | release                                 sign, verify and inspect the acquisition catalogue, its keyring and its compatibility manifest
  engine list | show | detect | bind | check | preview | run | stage | unstage             set up a Quake engine you already have, and start it as a supervised job
  game list | show | join | preview | host | stop                                          find a game somebody is hosting and join it, or advertise one of your own
  launch <game> [--map <name>] [--game-root <dir>] [--dry-run]                             launch a game as a supervised job, using its AUB launch config
  extractor status | plan | install | version                                               obtain and run the separately licensed auto-pigeon-extractor (AUE)
  migrate                                                                                  fold Launcher and older Companion configuration into the current one
  version                                                                                  print the build version
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
auto-pigeon.engine.darkplaces            engine  builtin   play_map, play_package, join_server, host_listen, host_dedicated
auto-pigeon.engine.fteqw                 engine  builtin   play_map, play_package, join_server, host_listen, host_dedicated
auto-pigeon.engine.ironwail              engine  builtin   play_map, play_package, join_server, host_listen
auto-pigeon.engine.q1-generic            engine  builtin   play_map, play_package, join_server, host_listen
auto-pigeon.engine.quakespasm            engine  builtin   play_map, play_package, join_server, host_listen
auto-pigeon.engine.quakespasm-spiked     engine  builtin   play_map, play_package, join_server, host_listen, host_dedicated
auto-pigeon.engine.vkquake               engine  builtin   play_map, play_package, join_server, host_listen
auto-pigeon.ericw-tools.q1               tool    builtin   compile, vis, light, inspect, check
auto-pigeon.q1.fast-preview              pipeline builtin
auto-pigeon.q1.final                     pipeline builtin
auto-pigeon.q1.normal                    pipeline builtin
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
    --profile auto-pigeon.ericw-tools.q1 --action compile \
    --root project_root=~/maps/level \
    --input source_map=~/maps/level/level.map --option basename=level
profile:  auto-pigeon.ericw-tools.q1 1.0.0 (builtin)
action:   compile
digest:   sha256:0a318124692ae01fb58f9ca132edc64e4a57315bb0b6d86c67b1a04ca82d4ef2
workdir:  ~/.cache/auto-pigeon-companion/jobs/20260907T020606Z-4aea09437265/workspace
command:  …/tools/entries/sha256-986531ff…/files/ericw-tools-v0.18.1-Linux/bin/qbsp -leaktest …/workspace/input/source_map/level.map …/workspace/level.bsp
argv:
  [0] …/tools/entries/sha256-986531ff…/files/ericw-tools-v0.18.1-Linux/bin/qbsp
  [1] -leaktest
  [2] …/jobs/20260907T020606Z-4aea09437265/workspace/input/source_map/level.map
  [3] …/jobs/20260907T020606Z-4aea09437265/workspace/level.bsp
environment:
  HOME=…/jobs/20260907T020606Z-4aea09437265/home
  LANG=C
  LC_ALL=C
  PWD=…/jobs/20260907T020606Z-4aea09437265/workspace
  TEMP=…/jobs/20260907T020606Z-4aea09437265/tmp
  TMP=…/jobs/20260907T020606Z-4aea09437265/tmp
  TMPDIR=…/jobs/20260907T020606Z-4aea09437265/tmp
  USERPROFILE=…/jobs/20260907T020606Z-4aea09437265/home
```

`--root project_root=…` is not optional once a tool profile is bound. Declared
roots are what a job's inputs are allowed to come from, so a map source outside
every one of them is refused rather than staged — and `qbsp` needs that
directory anyway, because the WAD a map names in its worldspawn is found beside
the `.map`. `companion build` supplies its own root and copies your files into
it, which is why the section below needs no `--root`.

**Then run it.** The program's output reaches your terminal as it is produced
*and* goes into the job's bounded log; that is one execution with two readers,
not a streaming mode beside a recording one.

```console
$ companion job run \
    --profile auto-pigeon.engine.quakespasm --action play_map \
    --executable engine=/bin/echo --root game_root=/games/quake \
    --root content_root=/games/quake \
    --runtime map_name=level --runtime mod_name=mymap
job 20260906T235206Z-6d2022e01cd7 queued
-basedir /games/quake -game mymap +map level
job 20260906T235206Z-6d2022e01cd7: succeeded — finished, and every required output was produced
  auto-pigeon.engine.quakespasm play_map (client), 1ms
  exit status 0
```

(`/bin/echo` stands in for a real engine above, so the example runs on a machine
with no game installed and prints the argument array the engine would have got.
`companion engine` — below — is the shorter way to say the same thing once an
engine is set up.)

**Everything a job did is still there afterwards.**

```console
$ companion job list
20260906T235206Z-6d2022e01cd7  succeeded    1ms        auto-pigeon.engine.quakespasm play_map (client)

$ companion job logs 20260906T235206Z-6d2022e01cd7
-basedir /games/quake -game mymap +map level

$ companion job artifacts 20260906T235206Z-6d2022e01cd7
no artifacts
```

`--wait=false` on `job run` and `job retry` does not do what it looks like it
does, and now says so. The executor is in the command's own process, so a
command that submits a job and returns runs its own shutdown, and the job is
recorded `interrupted` without a process ever having started. The flag has
shipped, so it is not removed from under a script here — it warns. `companion
engine run` has no such flag for the same reason.

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

Why there is exactly one executor, and why a crashed job is admitted rather than
repaired, is [ADR-0003](docs/adr/0003-one-executor-and-the-record-is-what-says-a-job-ran.md).

### Building a map

`job run` runs one program. `build run` runs a **pipeline**: several programs in
order, with the files wired between them and a record of what happened. Every
process it starts is still a job — same queue, same supervision, same logs, and
`companion job show` finds each one by the id the build printed. The build adds
ordering, wiring and evidence, and nothing else.

Three pipelines ship, and they are the same three stages with different options:

```console
$ companion build pipelines
auto-pigeon.q1.fast-preview      1.0.0    builtin   compile -> vis -> light  ready
auto-pigeon.q1.final             1.0.0    builtin   compile -> vis -> light  ready
auto-pigeon.q1.normal            1.0.0    builtin   compile -> vis -> light  ready
```

`fast-preview` runs a rough visibility pass and unsupersampled lighting, for the
loop where you are moving a wall and want to see it. `normal` is the full
visibility pass with coloured lightmaps. `final` adds 4x supersampling,
softening and a bounce, and is measured in hours on a real map rather than
seconds. `ready` means every capability the pipeline needs is provided by
something installed; anything else says what is missing.

**Preview first.** Every stage is resolved, every option is checked against the
tool's own declaration, and nothing runs:

```console
$ companion build preview --pipeline auto-pigeon.q1.normal \
    --input source_map=~/maps/level/level.map --input wad=~/maps/level/level.wad
pipeline  auto-pigeon.q1.normal 1.0.0 (builtin)
          sha256:51b2672eb2746f1d77ccc490a3bcfac088f9d1941fd3c9b1e4157965f56c6151
tool      auto-pigeon.ericw-tools.q1 1.0.0 — ericw-tools 0.18.1 (Quake 1) 0.18.1

step compile — Compile the map
  capability q1.bsp.compile -> auto-pigeon.ericw-tools.q1/compile
  in <workspace>
  …/ericw-tools-v0.18.1-Linux/bin/qbsp -leaktest <workspace>/input/source_map/level.map <workspace>/level.bsp
  (predicted: resolved against where this build would stage each file, not against files that exist)

step vis — Compute visibility
  capability q1.bsp.vis -> auto-pigeon.ericw-tools.q1/vis
  in <workspace>
  …/ericw-tools-v0.18.1-Linux/bin/vis -threads 4 -level 4 <workspace>/input/bsp/level.bsp
  (predicted: resolved against where this build would stage each file, not against files that exist)

step light — Compute lighting
  capability q1.bsp.light -> auto-pigeon.ericw-tools.q1/light
  in <workspace>
  …/ericw-tools-v0.18.1-Linux/bin/light -threads 4 -lit <workspace>/input/bsp/level.bsp
  (predicted: resolved against where this build would stage each file, not against files that exist)
```

The placeholders are the honest part. Only the first stage's inputs exist yet,
so the later ones are resolved against the paths this build *would* stage their
inputs at. That claim is not left as a claim: a real build previews each step
through the executor with the identical request immediately before submitting
it, compares the two argvs after substituting the job directory a preview gets
for the one the run gets, and **fails the build** if they differ. That is what
`preview matched: true` below is.

**Then build it.**

```console
$ companion build run --pipeline auto-pigeon.q1.normal \
    --input source_map=~/maps/level/level.map --input wad=~/maps/level/level.wad

build 20260907T020638Z-6a2b0ee8 — succeeded
  pipeline  auto-pigeon.q1.normal 1.0.0 (builtin)
  tool      ericw-tools 0.18.1 (Quake 1) 0.18.1 via managed_download
            pinned ericw-tools.q1 0.18.1 sha256:986531ff66d692fa732b7f75a6c871dcbd152b98721d1c2475b76d3367f040e2
            qbsp      sha256:8000b646b0af045974ca3997354227d215bd4ea384603d93545d72a34f15839b
            vis       sha256:4aef44413b7c6fd63411d8b32e1d4ad6a10944194b972754f69e80cd901a64c3
            light     sha256:bb150b6a105eff3adf843280676efb8b6341fc743dd8f2ca97656fb9064c3350
            bspinfo   sha256:b368e9341548897ee30d2573c58cad7873fc3da977645707ef17488e4d85ad7f
            bsputil   sha256:6a39c9cfeb1c44855d54f217f26c8148a5e834bcb43f923560354737ee7f6c38
  input     source_map   sha256:fe172908e8ee6cdcdc9330b78de97e4111a5547f6e3a6681676be7a10c95ebe2
  input     wad          sha256:2b08798bb8d3a19fbba26c2018b61ec527e95b571a921c56566982b5c696c29a

  step compile  succeeded       4ms  job 20260907T020638Z-f6a19e0f9b03
    …/bin/qbsp -leaktest …/workspace/input/source_map/level.map …/workspace/level.bsp
    preview matched: true
    info     A texture WAD was opened.
    progress Writing the BSP.
    bsp            2648  sha256:3093db35bc02717afc4ecf8f955f6558c681e7779dda47e99e63ea7245f0bf33
    prt               9  sha256:b1ac538e53efc28ace2088324b1c0504d0f09b013d30b39ce231d76124bc6c22
    log            3005  sha256:2964f58670edd05fbe78dab26651c35847ee8ae8621ead60c04195d1722a9283

  step vis      succeeded      72ms  job 20260907T020638Z-5ec98736bb9d
    …/bin/vis -threads 4 -level 4 …/workspace/input/bsp/level.bsp
    preview matched: true
    progress Visibility data written.
    bsp            2652  sha256:e6a36f45abb1616ff81d891dc3f54cc49c9e141cf4ae5215d407c72c51e7b290
    log             734  sha256:4bab59d5647b3a8075972455edfac70c7c5d69623403c1492c0c265fe6e52445

  step light    succeeded      76ms  job 20260907T020638Z-200477296d45
    …/bin/light -threads 4 -lit …/workspace/input/bsp/level.bsp
    preview matched: true
    progress light is writing a file.
    progress light is writing a file.
    info     light reported how many faces received no light.
    bsp            3980  sha256:5ed01da38b3fd3dd802d519934495aba1f54d45ce69c7e452ce7af1d988fe49e
    lit            3983  sha256:d2db934fda5b46dfce1d214de918bc02d25d1fce89cae2be667d5a197cafd7d7
    log            1530  sha256:ba7dffbb58224a0de8f495ac6ee3e003ceeec43eb244c27f69a2c991ed0ef5b9

  published
    bsp          ~/.cache/auto-pigeon-companion/builds/20260907T020638Z-6a2b0ee8/output/bsp/level.bsp
    lit          ~/.cache/auto-pigeon-companion/builds/20260907T020638Z-6a2b0ee8/output/lit/level.lit
    pts          not produced (optional)
    compile_log  ~/.cache/auto-pigeon-companion/builds/20260907T020638Z-6a2b0ee8/output/compile_log/level.log
    vis_log      ~/.cache/auto-pigeon-companion/builds/20260907T020638Z-6a2b0ee8/output/vis_log/vis.log
    light_log    ~/.cache/auto-pigeon-companion/builds/20260907T020638Z-6a2b0ee8/output/light_log/light.log

  recipe key       sha256:63b44509b884b5319b9ddde2c4f2ee765d97641a105a9f4388f6837e0cac7eee
  manifest         ~/.cache/auto-pigeon-companion/builds/20260907T020638Z-6a2b0ee8/manifest.json
```

Options are overridden per stage, because a pipeline has several and an
unqualified name would be the command guessing which one you meant:
`--option light.sampling=extra4 --option vis.threads=1`. Every one of them is a
knob the tool's author declared, with a type and a range; there is no free-text
argument box, which is why the command you approved above is the command that
ran.

`companion build list` and `companion build show <id>` read a build afterwards;
`--json` prints the manifest for a script.

#### When it goes wrong

A build that fails says which stage, keeps everything that stage wrote, and does
not publish a BSP that nobody should ship. A map with a hole in it:

```console
$ companion build run --pipeline auto-pigeon.q1.normal --input source_map=~/maps/level/leak.map
build 20260907T020653Z-7b3f4a6f — failed
  error     the compile step: job: qbsp exited with status 1

  step compile  failed          2ms  job 20260907T020653Z-da3febcf4d2d
    preview matched: true
    error: job: qbsp exited with status 1
    error    The map leaks: there is a gap between the inside and the void.
             Load the .pts point file this build published in your editor and follow it to the hole.
    info     A point file was written; it is the `pts` artifact of this build.
    error    qbsp stopped rather than write a BSP for a map that leaks.
             Turn the leak test off if you want the BSP anyway; it will have no visibility data.
    bsp        not produced
    pts            8284  sha256:3ada70a7adf202dd460e797fdacc37d6b5021d13c9261c584026b43143f05435

  step vis      skipped         0ms
  step light    skipped         0ms

  published
    bsp          not produced
    pts          ~/.cache/auto-pigeon-companion/builds/20260907T020653Z-7b3f4a6f/output/pts/level.pts
    compile_log  ~/.cache/auto-pigeon-companion/builds/20260907T020653Z-7b3f4a6f/output/compile_log/level.log
```

The point file is published *because* the build failed: it is the one artifact
that says where the hole is. The stages that never ran say so rather than being
absent from the record. `job logs <id>` on any stage's job id gives the
compiler's own words, whole and unfiltered — the diagnostics above classify, and
never suppress.

`--strict` is the other half. `qbsp` builds a BSP for a map whose textures it
could not find, and says so twice; by its own verdict that is a success, and by
anybody else's it is a broken map. The default respects the tool's verdict and
records the finding; `--strict` turns any error-severity diagnostic into a failed
build, naming the stage and the rule, which is what a gate wants:

```console
$ companion build run --strict --pipeline auto-pigeon.q1.normal --input source_map=~/maps/level/level.map
build 20260907T014243Z-82395e03 — failed
  error     --strict: the compile step reported no_wad: The map names no WAD the compiler could open, so every face is untextured.
```

#### What a manifest is for, and what "reproducible" means here

`manifest.json` answers *what produced this BSP* without needing the machine
that produced it: the pipeline document's digest, each stage's exact argv, the
SHA-256 of every executable that ran, of every input and of every output, and
every diagnostic the tools emitted. The **recipe key** is a digest over the
subset of that which determines the result — the documents, the tools, the
inputs, the options and the argv shape — with the paths, times and job ids that
differ between two runs of one build deliberately left out. Two builds with the
same key asked the same tools to do the same thing to the same bytes.

It does not cover the outputs, and that is measured rather than cautious. Of
ericw-tools 0.18.1 on this workspace's pinned build: `qbsp` and `vis` are
byte-identical run to run, and **`light` is not, above one thread** — three runs
of one map at `-threads 4` produced three different lightmaps, and `-threads 1`
produced the same one three times. (`auto-pigeon-tools` measured the same thing
independently on `20260901`, for its own acceptance suite.) A key that included
the outputs would report every ordinary build as irreproducible, which is a true
statement about a thread pool and a useless one about the build. So:
`--option light.threads=1` is what a bit-reproducible build costs, and the
digest of every output is recorded beside it either way.

An offline rebuild needs nothing but the cache: the tools were verified when they
were installed, the install record says by whom, and `AUCOM_OFFLINE=1` changes
what is *available*, not what is checked. See [Offline](#offline).

Why a pipeline is several jobs rather than one job with several processes, and
what that costs, is
[ADR-0005](docs/adr/0005-a-pipeline-is-several-jobs-and-a-manifest-is-what-says-so.md).

### Packaging: PAK and PK3

A compiled BSP is not yet something anybody can install. `companion package`
turns it into the archive the engine reads — a **PAK** for Quake and Quake II, a
**PK3** for Quake III — and writes a manifest beside it saying what went in.

```console
$ companion package targets
quake-pak    Quake PAK (id1-compatible)
  format pak, store by default, reproducibility portable
  at most 2048 members (MAX_FILES_IN_PACK in the original Quake source; modern engines raise it, id-era ones do not), member paths up to 55 bytes
  Auto-Pigeon metadata inside the archive: not permitted; the manifest is written beside the archive

quake2-pak   Quake II PAK
  format pak, store by default, reproducibility portable
  ...

quake3-pk3   Quake III PK3 (ZIP)
  format pk3, deflate by default, reproducibility per_build
  ...
```

Nothing is written until you have seen what would be. `preview` decides and
prints; `create` decides again from the same flags and writes, refusing anything
the preview flagged:

```console
$ companion package preview --target quake-pak --from ~/maps/mymap
target quake-pak — pak, store, reproducibility portable
2 file(s) selected, 34 B to package

  gfx/palette.lmp       15 B  authored      authored-root
    from /home/you/maps/mymap/gfx/palette.lmp
    it came from /home/you/maps/mymap, which you declared as your own content
  maps/e1m1.bsp         19 B  authored      authored-root
    from /home/you/maps/mymap/maps/e1m1.bsp
    it came from /home/you/maps/mymap, which you declared as your own content

2 to package, 0 awaiting review, 0 refused

$ companion package create --target quake-pak --from ~/maps/mymap --out ~/mymap.pak --label "my first map"
wrote /home/you/mymap.pak
      /home/you/mymap.pak.package.json
mymap.pak  quake-pak  2 members, 174 B
  reproducibility: portable — the same members produce the same bytes on any machine and under any build of the Companion: this container carries no timestamp, no permission bits and no compressor
```

`--build <id>` packages what a pipeline produced, and carries that build's
pipeline digest, recipe key and tool digests into the package manifest, so the
archive answers *what compiled this* without the machine that compiled it:

```console
$ companion build run --pipeline aucom.pipeline.ericw-q1 --input map=mymap.map
$ companion package create --target quake-pak --build 20260907T075808Z-1a2b3c4d --out ~/mymap.pak
```

Reading an archive is two commands, and the split matters. `inspect` reads the
directory and decompresses nothing — it is what you run on a file you do not
trust yet. `verify` reads every member, checks it against what the directory
declared, and compares the whole thing with the sidecar if there is one:

```console
$ companion package inspect ~/mymap.pak
mymap.pak  pak  2 members, 174 B stored, 34 B of contents
sha256:90229e3fb149e918d50d15568e06f618b58ec3a2962e39211c870b8a6e091a72

          15  store    gfx/palette.lmp
          19  store    maps/e1m1.bsp

$ companion package verify ~/mymap.pak
...
verified 2 of 2 members
  manifest: /home/you/mymap.pak.package.json (agrees)

$ companion package extract ~/mymap.pak --dest ~/unpacked
extracted 2 file(s), 34 bytes, into /home/you/unpacked
  gfx/palette.lmp
  maps/e1m1.bsp
```

#### What "reproducible" means for an archive

Two promises, and the manifest says which one it is making rather than leaving
you to infer it.

**`portable`** — PAK, and PK3 with `--compression store`. The same members
produce the same bytes on any machine and under any build of the Companion.
Neither container carries a timestamp, a permission bit or a compressor, so
there is nothing in the byte stream that a machine can put its fingerprints on.
Demonstrated rather than asserted:

```console
$ companion package create --target quake-pak --from ~/maps/mymap --out /tmp/a.pak
$ touch -d 2011-01-01 ~/maps/mymap/maps/e1m1.bsp && chmod 600 ~/maps/mymap/gfx/palette.lmp
$ companion package create --target quake-pak --from ~/maps/mymap --out /tmp/b.pak
$ sha256sum /tmp/a.pak /tmp/b.pak
90229e3fb149e918d50d15568e06f618b58ec3a2962e39211c870b8a6e091a72  /tmp/a.pak
90229e3fb149e918d50d15568e06f618b58ec3a2962e39211c870b8a6e091a72  /tmp/b.pak
```

**`per_build`** — PK3 with the default `deflate`. The same members produce the
same bytes under one build of the Companion, on any operating system, and may
differ under another. The compressor is Go's, and its output is a property of
that library's version rather than of this program. That is the honest scope of
the claim; `--compression store` buys the stronger one at the cost of size.

The test suite proves both. It cannot run on three operating systems at once, so
instead it varies *everything that differs between them* — modification times,
permission bits, path separators, directory walk order and the absolute path of
the source tree — and asserts the archive does not move, with the two `portable`
digests pinned as golden constants.

#### Asset safety, and what this policy does not claim

The working directory for making a Quake map is very often the directory the
game is installed in, so a `--from .` that sweeps it up will happily package
id Software's content alongside yours. Preventing that is what the policy is
for, and *how* it does it is the part worth reading.

It is **not** a list of forbidden filenames. That would be easy and wrong in
both directions: it refuses your own `progs.dat` from a total conversion you
wrote, it passes id's `e1m1.bsp` the moment somebody renames it, and — worst —
it implies a legal conclusion that a filename cannot support.

Instead every candidate is classified by **where its bytes came from**, and the
rules are ordered by strength of evidence. First match wins:

| Rule | Evidence | Verdict |
| --- | --- | --- |
| `known-asset-digest` | its SHA-256 is a released commercial file's | **refuse** |
| `authorized-known-asset` | the same, and you asserted the right to distribute it | include, both facts recorded |
| `authorized` | you asserted the right, for a file the corpus did not identify | include |
| `build-output` | a build manifest records this exact content | include |
| `game-content-root` | it was selected out of an installed game's directory | **review** |
| `authored-root` | it came from a directory you declared as your own | include |
| `unknown-provenance` | nothing above applies | **review** |

`review` is the normal outcome for a file this program knows nothing about, and
it is neither an accusation nor a refusal — it is an unanswered question, held
until somebody answers it. `create` will not write a package with one
outstanding.

Your configured game roots are used automatically, without a flag, because the
accident this exists to catch is one you have not yet noticed:

```console
$ companion package preview --target quake-pak --from ~/maps/mymap --from-at id1=/games/quake/id1
? id1/pak0.pak          23 B  game_content  game-content-root
    from /games/quake/id1/pak0.pak
    it was selected out of /games/quake, which is an installed game's content directory. That says where it was found, not who wrote it — your own mod lives there too — so it needs a look before it ships
    hint: "pak0.pak" is the naming id Software used for the archives it shipped; that is a name, and it decides nothing
    hint: a path element is "id1", which is Quake's content directory; a directory name is not evidence about a file's author
...
2 to package, 1 awaiting review, 0 refused

this plan will not be written as it stands:
  pack: 1 file(s) need review before they can be packaged, starting with "id1/pak0.pak" — …
```

Filenames appear only as **hints**, which explain a decision made on other
grounds and say so in their own text. Resolve a held file by narrowing the
selection, or by accepting it explicitly:

```console
$ companion package create --target quake-pak --from ~/maps/mymap --out ~/mymap.pak \
    --acknowledge id1/pak0.pak
$ companion package create --target quake-pak --from ~/maps/mymap --out ~/mymap.pak \
    --authorize id1/pak0.pak --reason "distribution licence, reference 12345"
```

`--authorize` and `--acknowledge-all` require `--reason`, and the reason is
recorded verbatim in the manifest. That sentence is the point of the whole
mechanism: it is what somebody stands behind.

The exact-identification corpus (`--known-assets <file>`, or `known-assets.json`
in the configuration directory) **ships empty**, deliberately. A list of
plausible-looking hashes copied from somewhere would be a rule that never fires
while looking like one that does, and this repository has computed no digests
from released media. The protection comes from the two `review` rules, which
need no corpus. A supplied corpus must declare its own `source`, because a
digest list that does not say who computed it cannot be argued with:

```json
{
  "schema_version": "aucom.known-assets/1.0",
  "source": "digests computed from a retail Quake CD, 2026-09-06, by <who>",
  "assets": [
    {"sha256": "sha256:…", "release": "Quake 1.06 registered, id1/pak1.pak"}
  ]
}
```

None of this is a legal opinion, and the program does not offer one. It reports
what it knows about where bytes came from, and it declines to guess when it
knows nothing. Why provenance rather than filenames, what it costs and what was
rejected, is
[ADR-0006](docs/adr/0006-provenance-decides-what-is-packaged-not-filenames.md).

#### The sidecar, and what never goes inside the archive

The package manifest is written beside the archive as
`<archive>.package.json` — never inside it. An engine walking a PAK for
`progs.dat` has no use for a manifest, and a PK3 with an `aucom/` directory
publishes your toolchain to everyone who downloads the map. **No built-in target
permits Auto-Pigeon metadata inside a game archive**; the mechanism exists for a
profile-supplied target that says otherwise, and `--embed-manifest` is refused
with an explanation rather than silently ignored.

The sidecar carries the archive's digest and size, every member with its own
digest and the decision that let it in, the review record, the build reference
and recipe key, and each tool profile's id, version, digest and executable
digests. `companion package verify` compares it against the archive and reports
every disagreement, not just the first.

#### What is refused, in both directions

The same rules apply to what is written and to what is read, and none of them
repairs anything — an archive member named `../../.ssh/authorized_keys` is not a
malformed name to be tidied into a safe one, it is a document that has said what
it is for.

- Absolute paths, Windows drive and UNC paths, `~`, `..` and `.` elements, empty
  elements, NUL bytes, backslashes, and anything outside printable ASCII.
- Reserved Windows device names (`aux.bsp` fails the way `aux` does), and
  elements Windows cannot store as written.
- Symbolic links, devices, sockets and pipes — in a source tree being packaged,
  and as members of an archive being read.
- Duplicate member paths, and paths differing only in capitalisation: two files
  on Linux and one on Windows and macOS, so packing one is choosing which half
  of your audience gets the wrong map.
- Extraction outside the destination, including through a symbolic link that was
  *already sitting in* the destination before extraction started.
- Overwriting anything implicitly — an existing archive, an existing extracted
  file, or a source file the package is about to read. Each needs `--replace`,
  and packaging a directory into itself is refused outright.
- Structurally impossible archives: a PAK directory that is not a whole number
  of 64-byte records, offsets that point past the end of the file or into the
  header, a name field with no terminator, member data that overlaps the
  directory, a PK3 member whose data disagrees with its declared length or its
  own CRC.

Bombs are refused **on the declaration, before anything is allocated**: entry
count, per-member size, total size, and the ratio of declared contents to the
archive's own length. The two containers get bombed differently — a PK3 lies
with its compression ratio, and a PAK, which cannot compress, lies with its
*offsets* instead, pointing a hundred thousand directory records at one blob.
Both are caught by the same budget, because it sums what the directory declares.

The archive is staged in the destination directory and renamed into place, so
the output path never holds a half-written archive.

#### q1tools, and what this does not replace

`q1tools` and QPakMan are perfectly good **interactive** PAK tools, and if you
want to browse an archive, drag files around and rebuild it by hand, use one of
them. (Neither is bundled, and neither is pinned here: this repository does not
carry a URL for a tool it does not download. `AUCOM 217` is the task that gives
external community tools a catalogue entry of their own.) No q1tools or QPakMan
code was copied or translated into this repository; the formats here are
implemented from their published structures.

What `companion package` is for instead is the *unattended* half: a packaging
step that produces the same bytes twice, refuses what it cannot vouch for, and
leaves a manifest saying what it did. Those are different jobs, and there is no
reason to do only one of them.

### Engines

The Companion starts Quake engines, and it knows nothing about any of them. An
engine is an **engine profile** — the same document format as a tool profile,
resolved by the same resolver, run by the same executor — and seven of them
ship inside the binary:

```console
$ companion engine list
auto-pigeon.engine.darkplaces        beta         unverified  not set up here
  play_map, play_package, join_server, host_listen, host_dedicated
  linux/amd64: Upstream publishes Linux builds and several distributions package it; nobody here has run one.
auto-pigeon.engine.fteqw             rolling      unverified  not set up here
  play_map, play_package, join_server, host_listen, host_dedicated
  linux/amd64: Upstream publishes Linux autobuilds; nobody here has run one.
auto-pigeon.engine.ironwail          0.7.x        unverified  not set up here
  play_map, play_package, join_server, host_listen
  linux/amd64: Ironwail builds from source on Linux and upstream publishes no Linux binary; nobody here has run one.
auto-pigeon.engine.q1-generic        unknown      unverified  not set up here
  play_map, play_package, join_server, host_listen
  linux/amd64: Nothing is known about the engine this profile is pointed at.
auto-pigeon.engine.quakespasm        0.94.x       unverified  not set up here
  play_map, play_package, join_server, host_listen
  linux/amd64: Upstream publishes source and several distributions package it; nobody here has run one.
auto-pigeon.engine.quakespasm-spiked 0.94.x-spiked unverified  not set up here
  play_map, play_package, join_server, host_listen, host_dedicated
  linux/amd64: Upstream publishes Linux builds; nobody here has run one.
auto-pigeon.engine.vkquake           1.30.x       unverified  not set up here
  play_map, play_package, join_server, host_listen
  linux/amd64: Upstream publishes source and several distributions package it; nobody here has run one.
```

Two things in that listing are the whole point.

**Nothing says `supported`.** Each profile's command line comes from that
engine's own published documentation, and no build of any of the six has been
run by this project — six upstream projects across three operating systems,
none of which starts without a copy of Quake that is not ours to distribute. So
every platform is `unverified` with a note saying exactly that. `last_qualified`
records the date of the last check; what kind of check it was is what the
platform status beside it says. A profile claiming `supported` on this evidence
would be the plausible-looking wrong answer this repository keeps not giving.

**The action lists differ, and they differ because the engines do.** QuakeSpasm,
Ironwail and vkQuake are clients: they have no `host_dedicated`, because they do
not run a dedicated server, and a profile that offered the action anyway would
be a button that starts something else. QuakeSpasm-Spiked has one — the FTE
networking is the reason it exists as a separate project. An engine profile says
what its engine does by declaring it, and says what it does not by leaving it
out.

`companion engine show` is the long form, including what a profile will not
claim:

```console
$ companion engine show auto-pigeon.engine.ironwail
Ironwail 0.7.x (auto-pigeon.engine.ironwail)
  A high-performance QuakeSpasm fork for modern GPUs. Windows and Linux; not macOS.
  Built in — shipped with this version of the Companion.
  upstream: https://github.com/andrei-drexler/ironwail
  licence:  GPL-2.0-or-later
  checked:  2026-09-07 against Ironwail 0.7.x

platforms:
    windows/amd64    unverified  Upstream publishes Windows builds; nobody here has run one. The command line is Ironwail's own documented one.
  * linux/amd64      unverified  Ironwail builds from source on Linux and upstream publishes no Linux binary; nobody here has run one.
    darwin/amd64     unsupported Upstream ships no macOS build, and Ironwail requires an OpenGL core context newer than the one macOS provides.
    darwin/arm64     unsupported Upstream ships no macOS build, and Ironwail requires an OpenGL core context newer than the one macOS provides.

actions:
  play_map         client            Play a map
  play_package     client            Play a mod or package
  join_server      client            Join a server
  host_listen      listen_server     Host a game and play in it
  not offered: host_dedicated — this engine does not do it

content:
  id1        loose_files    game_root/id1
  pak        pak            game_root/id1
  mod        mod_directory  game_root

on this machine:
  nothing recorded — `companion engine bind auto-pigeon.engine.ironwail --engine <path>`
```

#### Three separate things: the engine, the game, and your project

A binding records them separately, because they are separate:

| Root | What it is |
| --- | --- |
| the executable | the engine program you installed, wherever that is |
| `game_root` | the directory containing `id1` — your own copy of Quake |
| `content_root` | your project, which is not inside the game and should not be |

**The Companion never obtains the middle one.** Quake's data is a commercial
release; it is not redistributable, and nothing here copies, downloads or
uploads it. What it will do is guess where you already have it:

```console
$ companion engine detect
steam    /home/you/.local/share/Steam/steamapps/common/Quake
         found Id1/PAK0.PAK
steam    /home/you/.local/share/Steam/steamapps/common/Quake/rerelease
         found id1/pak0.pak
         The re-release's own copy of the game data.
gog      /home/you/GOG Games/Quake
         found id1/pak0.pak

Nothing was recorded. These are guesses about your machine, not decisions:
pass the one you want to `companion engine bind ... --game-root <path>`.
```

Detection reads directory entries and stops there. It opens no game file, and
it writes nothing at all — the path becomes a binding when you type it into
`engine bind`, and not before. It also matches case-insensitively and reports
the spelling that is really on disk, because Quake's own releases ship both
`id1/pak0.pak` and `Id1/PAK0.PAK`, and on a case-sensitive filesystem an engine
handed the wrong one finds no game.

Add `--near <dir>` for a hand-installed copy, which is where most people who
have owned Quake for twenty years keep it.

```console
$ companion engine bind auto-pigeon.engine.quakespasm \
    --engine /usr/games/quakespasm \
    --game-root ~/games/quake \
    --content-root ~/maps/mylevel
recorded for auto-pigeon.engine.quakespasm:
  engine         /usr/games/quakespasm
  content_root   /home/you/maps/mylevel
  game_root      /home/you/games/quake
```

A binding records two different kinds of fact, and they expire differently.
Where the engine and the game are is about *this machine*, and it survives the
document changing. The approval is about *that document*, so a new version of
the profile needs a new decision — `engine show` then says the approval covers
an earlier version, and `engine bind <id> --approve` is enough to give it: it
does not ask for the paths again.

A profile may also declare a root neither `--game-root` nor `--content-root`
names. `--root <role>=<path>` sets any of them, which is what makes a
user-authored engine profile bindable without a new flag in this program.

#### What would stop it, before anything starts

```console
$ companion engine check auto-pigeon.engine.quakespasm --action play_map
auto-pigeon.engine.quakespasm play_map would run here.

$ companion engine check auto-pigeon.engine.quakespasm --action host_dedicated
QuakeSpasm has no "host_dedicated" action.
  An engine profile leaves out what its engine does not do, so host_dedicated is not
  something QuakeSpasm does. It offers: host_listen, join_server, play_map, play_package.
```

`check` names six other things the same way, each with what to do about it: an
engine executable that has been moved or uninstalled, a game root with no `id1`
in it, an `Id1` where the engine wants `id1`, a platform the profile calls
`unsupported`, a root nothing has ever set, and a binding written against a
version of the document that has since changed. It is the same list `engine run`
refuses on and `engine preview` prints beside the command, so the answer to "why
can I not press play" arrives before the button does anything.

#### Previewing, and then playing

The preview is the same resolution the run does, so what you approve is what
starts. The argument array is printed one element per line, because where each
argument begins and ends is the whole point of not having a shell:

```console
$ companion engine preview auto-pigeon.engine.quakespasm \
    --action play_map --map level --mod mymap
profile:  auto-pigeon.engine.quakespasm 1.0.0 (builtin)
action:   play_map (client)
digest:   sha256:0dda1148f9c2082f3ce15bdd4e906e7917f14df6c3ee26ed7a7d2a23467bc5ca
workdir:  /home/you/games/quake
command:  /usr/games/quakespasm -basedir /home/you/games/quake -game mymap +map level
argv:
  [0] /usr/games/quakespasm
  [1] -basedir
  [2] /home/you/games/quake
  [3] -game
  [4] mymap
  [5] +map
  [6] level
environment:
  HOME=/home/you/.cache/auto-pigeon-companion/jobs/20260907T094345Z-10eccf444670/home
  LANG=C
  LC_ALL=C
  PWD=/home/you/games/quake
  …
```

`play_map` names a game directory as well as a map. That is not a Companion
invention — it is what `-game` is for in every one of these engines — and it
means the Companion never launches a map into a directory nobody named.

Starting it is a job, so it is queued, supervised, cancellable and recorded like
a compile:

```console
$ companion engine run auto-pigeon.engine.quakespasm \
    --action play_map --map level --mod mymap
job 20260907T094345Z-15ca5e0e3abb: play_map (client)
…
job 20260907T094345Z-15ca5e0e3abb: succeeded — finished, and every required output was produced
  auto-pigeon.engine.quakespasm play_map (client), 1ms
  exit status 0

$ companion job list
20260907T094345Z-15ca5e0e3abb  succeeded    1ms        auto-pigeon.engine.quakespasm play_map (client)
```

#### Client, listen server, dedicated server

The `(client)` on those lines is a recorded field and not a label. Each engine
action declares a **session role**, the mapping from action to role is fixed
rather than authored, and the role is written into the job record:

| Action | Session role |
| --- | --- |
| `play_map`, `play_package`, `join_server` | `client` |
| `host_listen` | `listen_server` |
| `host_dedicated` | `dedicated_server` |

Hosting is also a separate, high-risk permission from running the engine at all,
so approving "start this engine" is not approving "accept connections from the
internet". A job list that showed every running process the same way would be a
list in which you cannot tell whether your machine is currently reachable — so
`job list` says `(listen server — other people can join this machine)` when it
is.

#### Staging: getting what you built where the engine looks

An engine loads content from a directory beside `id1`, and `-game` does not take
a path outside the game root. Your project is somewhere else entirely, so
something has to copy it in — and, more importantly, take it away again.

```console
$ companion engine stage --game-root ~/games/quake --mod mymap --from ~/maps/mylevel/out --dry-run
maps/level.bsp
maps/level.lit
2 file(s) would be copied into /home/you/games/quake/mymap

$ companion engine stage --game-root ~/games/quake --mod mymap --from ~/maps/mylevel/out
staged 2 file(s) into /home/you/games/quake/mymap
start the engine with --mod mymap; `companion engine unstage` removes exactly these files
```

`companion engine run --stage <dir> --mod <name>` does both around one launch,
and removes the staged copy when the game exits unless you pass
`--keep-staged`. `engine run` always waits for the game — there is no
submit-and-return mode, because the executor lives in that process and a job
nothing is waiting for is a job nothing runs. Ctrl-C stops the game and its
whole process tree.

The removal is the part worth being careful about, and it is:

- staging writes a `.auto-pigeon-staged.json` record listing every file it
  wrote, **with each one's digest**;
- `unstage` removes exactly those files, and **skips any whose contents have
  changed** — if you edited a staged file, or dropped one of your own in beside
  it, you keep it, and the command says which;
- re-staging over a file you had edited is what you asked for and is still said
  out loud, one warning per file;
- a copy that fails partway undoes itself, so a half-written directory with no
  record in it never exists;
- a directory the Companion did not create is refused outright, with no flag to
  force it;
- `id1`, `qw`, `hipnotic`, `rogue`, `dopa` and `rerelease` cannot be staged
  into at all. Writing your project over the base game and then "cleaning it up"
  is the single most expensive mistake this feature could make.

#### Adding an engine the Companion has never heard of

An engine profile is a document, so this needs no release and no Go code. The
repository keeps a worked example of one written by hand:

```console
$ companion profile validate internal/engine/testdata/user-q1-engine.engine.json
internal/engine/testdata/user-q1-engine.engine.json: valid engine profile example.engines.quakespasm-of-my-own 0.3.1
  sha256:b759281c01c70ac572d751e7ffc1520a2a979135f5b47b9737de8f2f93258286

$ companion profile show internal/engine/testdata/user-q1-engine.engine.json
My own Quake engine 0.3.1 (example.engines.quakespasm-of-my-own)
  published by A Companion user, under GPL-2.0-or-later
  Community — imported from elsewhere; nobody has checked it for you.

  If you approve it, it may:
    - Run a dedicated game server on this computer that other people can connect to. [high]
    - Connect to hosts on the internet that this profile does not list. [high]
    - Run My own Quake engine (engine) as a program on your computer. [high]
    - Create and change files in the folder built content is published into. [high]
    - Read files in the folder built content is published into. [medium]
    - Read files in your installed game folder. [medium]

  digest: sha256:b759281c01c70ac572d751e7ffc1520a2a979135f5b47b9737de8f2f93258286

  Nothing here has been granted. Importing a profile does not let it do any of the above.
```

Hosting is the first line of that list because it is the most consequential
thing the document asks for, and it is asked for separately from running the
engine at all.

Copy such a file into your profile directory and it appears in `companion engine
list` beside the built-in ones, at `local` trust — a file that turned up in a
directory is not a file anybody vouched for, so it runs only once you have read
what it asks for and approved it:

```console
$ companion engine bind example.engines.quakespasm-of-my-own \
    --engine ~/src/myquake/myquake --game-root ~/games/quake \
    --content-root ~/maps/mylevel --approve
```

`TestAUserAuthoredEngineProfileLaunchesByTheSameRoute` is the test that keeps
this honest: it imports that document the ordinary way and launches it, and
there is no branch anywhere that asks whether a profile is built in.

#### How any of this is tested without a copy of Quake

`internal/enginefixture` is an engine that is not an engine: it records the
argv, the environment and the working directory it was started with, and then
becomes ready, crashes, or stays up until it is stopped. The acceptance tests
run every action of every profile against it and compare the recording, element
by element, with what the document said — including a game root called
`Quake — Ünïcode, spaces; $(id) && …`, which arrives as exactly one argument
because there is no shell anywhere in the path from the document to `execve`.

What that proves is that the Companion builds the command line it says it
builds. It cannot prove that Ironwail accepts that command line, and nothing
pretends otherwise — that is what `unverified` means.

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
enough to claim more. It answers "what does AUB say to run for this game"; the
curated documents under [Engines](#engines) answer "what does this engine
actually do", and that is the model. A document with the same id replaces a
generated stand-in by existing — the catalog prefers one to the other.

### Extractor

**Auto-Pigeon Extractor is a separate program under its own licence (AGPL-3.0).**
It is not part of this application, it is not inside this binary, and this
repository claims no licence over it. It is downloaded against the signed
catalogue, at the version a signed compatibility manifest names for this
Companion on this platform, and run as its own process.

It used to be embedded — the build copied a platform's AUE binary into
`internal/aue/embedded/` and `//go:embed` compiled it in. Three things were
wrong with that, and they are different kinds of wrong:

1. **Licensing.** An MIT artifact contained and appeared to cover an AGPL
   program, and a user holding the Companion had no way to tell whose bytes they
   were running or where to get their source.
2. **Verification.** Nothing checked the staged binary. `//go:embed` resolves at
   compile time, so a stale or wrong-platform file shipped silently and failed
   on the user's machine.
3. **Coupling.** A patched extractor needed a new Companion release.

```console
$ companion extractor status
extractor: Auto-Pigeon Extractor 1.171, verified
  required: 1.171 (invocation protocol 1.0 or later)

$ companion extractor plan
Auto-Pigeon Extractor 1.171, protocol 1.0 or later
already installed: 1.171 (sha256:49170ba6ee5d2200…)

$ companion extractor install
Auto-Pigeon Extractor 1.171 (linux/amd64), verified
/home/you/.cache/auto-pigeon-companion/packages/sha256-49170ba6…/files/auto-pigeon-extractor
digest  sha256:49170ba6ee5d220011c08ab010eaa84d784190878db5e8b28d311728f12a75f6
signed by k-9f2a… in catalogue auto-pigeon serial 7
invocation protocol 1.0 (this build requires at least 1.0)
licence AGPL-3.0-only — corresponding source: https://github.com/andrea-dintino/auto-pigeon-extractor
Auto-Pigeon Extractor is a separate program under its own licence. Running it as a subprocess does not make it part of the program that ran it, and does not relicense either one.

$ companion extractor version
1.171
```

#### There are exactly two ways to an executable

```text
managed             a verified cache entry, at the version the manifest names
developer override  AUCOM_AUE_BINARY, unverified, local, and labelled so
```

There is **no third, and no fallback between them.** A managed resolution that
fails is an error you read; it never quietly becomes an override, and an
override is never quietly treated as verified.

```console
$ companion extractor status
extractor: none — Auto-Pigeon Extractor 1.171 is required and is not installed

$ AUCOM_AUE_BINARY=../auto-pigeon-extractor/bin/auto-pigeon-extractor companion extractor status
extractor: UNVERIFIED developer override
  ../auto-pigeon-extractor/bin/auto-pigeon-extractor
  This extractor was named by AUCOM_AUE_BINARY. Nothing verified it: no catalogue signature, no digest, no compatibility rule. It is a local development override, it is never uploaded or published, and results produced with it are not results a managed extractor produced.
```

The override exists for development and for a support case where somebody must
be moved onto a patched build before a release. It is **local**: nothing
produced with it is uploaded, and every surface that describes an extractor says
it is unverified — including `/api/status`, which carries `aue_verified`
alongside `aue_available` because *there is one* and *it is the one we vouch for*
are different facts.

#### The protocol handshake, before anything else

A verified executable is not automatically one this build can talk to. The first
thing a resolved runner does is ask it `protocol --json` and compare what it
reports against the minimum the compatibility manifest declared: **the majors
must be equal and the minor at least the required one.** A later major is a
different contract, not a newer version of this one, and running it would mean
parsing its output against a contract that has been replaced.

That is why the manifest carries `min_protocol` at all. Without it the Companion
would be trusting a version *number* to imply a contract — exactly the
assumption a rebuilt or forked extractor breaks.

#### Every invocation is bounded

A timeout on the whole run (ten minutes by default, not per read: a process
printing one line every nine minutes keeps a per-read deadline satisfied for
ever). Cancellation by **SIGTERM first**, because the extractor's published
contract says a supervised run ends deliberately on one and writes a record
saying why. A fresh working directory per invocation, removed afterwards, so two
concurrent runs cannot see each other's scratch. And a bounded read, because a
subprocess is not a trusted producer of unbounded output.

#### Offline

Offline means no network and **no fewer checks**. The requirement comes from
`extractor-pin.json` — the last one this machine verified, recorded beside
`config.json` with the catalogue state — and the executable comes from the
cache, which is re-hashed against its install record on every use. The protocol
handshake still runs, against the recorded minimum.

A machine that has never resolved a requirement online says so rather than
guessing:

```console
$ companion extractor version --offline
error: catalog: offline: offline, and this machine has no recorded requirement for auto-pigeon.extractor on linux/amd64; run once with the network to resolve one
```

The fallback to the recorded answer happens **only** because you said
`--offline`. A verification that failed, a rollback attempt, an expired document
or an unreachable server is a refusal, and never becomes "use the older answer".

## Games people are hosting

`AUB/AUG/AUCOM 214`. `companion game` is two halves that must not be confused:
joining somebody else's server, and advertising one of your own.

**This program contacts no address but AUB's.** There is no server browser here,
no master-server client, and no probe of anybody's machine. The games you can see
are the ones people deliberately registered, and the reachability a listing
carries is AUB's — established from a machine that is not behind the host's own
NAT, which is why a check from here would establish only that this computer can
reach itself.

### Joining

```console
$ companion game list
gme000000000001  Friday deathmatch            public     live      Vera
gme000000000002  Coop night, bring a torch    unlisted   live      Sam

$ companion game join autopigeon://join/tkt1
Friday deathmatch
  hosted by  Vera
  address    203.0.113.4:26000 (verified)
  map        Sunken Chapel, revision 7
  verified   c0ffee1234567890…
  engine     auto-pigeon.engine.quakespasm (quakespasm)

This is what will run:
  quakespasm -basedir /games/quake +connect 203.0.113.4:26000
  in /games/quake

Nothing has been started. Add --approve to run the command above.
```

A join link is `autopigeon://join/<opaque-id>` and carries no token, no address
and no map id: everything is behind the id and is fetched over your own session,
so a link pasted into a chat window is worth nothing to anybody it was not minted
for. It is redeemable **once** and lasts two minutes.

Four things are checked before a command is offered, in this order:

1. **May this account have the map at all.** AUB answers it in the resolution, so
   you are told before anything is downloaded rather than half way through.
2. **Is there an engine for this.** Matched on the RUNTIME the host declared —
   `quakespasm`, `ironwail` — which is the thing two installations can agree
   about; the host's own profile id says nothing about what you have installed.
   Asked before the download, because refusing after fetching nine megabytes is
   the same refusal arrived at more expensively.
3. **Are the bytes the ones being played.** The revision is fetched through the
   asset cache, which verifies every file against AUB's declared digest, and the
   join link's own `map_content_sha256` is compared as well: two statements by
   two routes, and a join is where they have to agree.
4. **Do you approve the command.** The preview is the job service's own argv, not
   a re-rendering of it, so what you approved is what starts.

### Advertising a game you are hosting

Start the server first — `companion engine run … host_listen` — and advertise the
job that is serving it:

```console
$ companion game preview --map=$MAP --title='Friday deathmatch' \
    --engine=quakespasm --engine-version=0.96.3 \
    --endpoint=203.0.113.4:26000 --visibility=public
visibility  public
audience    Everybody signed in to this deployment can find this game in the listing.
endpoint    203.0.113.4:26000 (public)
reachable   unverified

This is everything the listing will say about you:
    title            Friday deathmatch     seen by everybody signed in
    host_nickname    your nickname         seen by everybody signed in
    map_name         Sunken Chapel         seen by everybody signed in
    …

Run the same command as `game host --job=<id> --confirm` to advertise it.

$ companion game host --job=$JOB --confirm --map=$MAP --title='Friday deathmatch' \
    --engine=quakespasm --endpoint=203.0.113.4:26000 --visibility=public
gme000000000001  Friday deathmatch  public  live  Vera
  beating every 30s while job jb-… runs; Ctrl-C to stop
```

**Nothing is advertised before the preview has been read.** `--confirm` is a
confirmation *of* the preview, and the preview is AUB's own computation run
without writing anything — so the fields you approve are the fields that get
published rather than a second rendering that could disagree.

**The advertisement ends when the process does.** The beat loop watches the
supervised job, and every way a job can end maps onto a word AUB has for it:
cancelling is `host_stopped`, a clean exit is `host_stopped`, a non-zero exit is
`host_crashed`, and being killed along with the Companion is `host_crashed`.
Signing out or quitting ends every advertisement with `owner_signed_out`.

What this program never sends is `heartbeat_missed`: that is the conclusion AUB
draws from its own clock when nothing got the chance to say anything, and a
client that could send it would be able to write a history that did not happen.

**A restart reclaims rather than re-registering.** The identity presented is a
digest of this installation's own directory, the map, and the address — stable
across the restart reclaim exists for, and different for a second server you are
entitled to run beside it. It is deliberately not a process id: a PID is
different after exactly the event reclaim exists for, and it is readable by
anything else on the machine.

**A LAN address is never published.** AUB classifies the endpoint before it
publishes anything, and an address on your own network can back a `private` game
only. If you want somebody outside to join, forward a UDP port on your router and
register the address it presents to the internet; nothing here will change your
router's configuration, and nothing here has opened a port.

## Assets from auto-pigeon-backend

`companion aub` lists and fetches the maps, WADs, entity catalogues, Game
Profiles and prefab packages your AUB account may build with, and pins an exact
revision of one so a build can be repeated.

It goes through AUB's versioned **Companion API** (`/api/companion/v1`) rather
than through PocketBase's collection API. That distinction is the whole point: a
program written against a collection listing is written against a *schema*, so
every column added at the backend becomes a compatibility question here. The
Companion API is a contract — a fixed vocabulary of asset types, a fixed shape
per answer, a version string in every response, and a capability document that
states the auth collection and the token lifetime *this* deployment configured
rather than the ones PocketBase documents.

### Ask what the backend offers

```console
$ companion aub capabilities
api version:   aub-companion-api/1.0
auth:          users
token lasts:   336h0m0s
revocable:     false
resumable:     false (range requests)
etag:          true
digests:       sha256
asset types:
  TYPE               REVISIONS       HISTORY  SCOPES
  entity_catalogue   revision_rows   true     owned,public,workspace
  game_profile       current_only    true     owned,public,workspace
  map                revision_rows   true     owned,public,member,workspace
  prefab_package     revision_rows   true     owned,workspace
  texture_source     revision_rows   true     owned,public,workspace
```

Three of those lines matter when something goes wrong.

**`revocable: false`** — AUB's token is a stateless JWT valid until it expires.
`companion auth logout` forgets it locally; it does not revoke it, and this
program says so rather than implying otherwise.

**`resumable: false`** — AUB keeps assets compressed at rest as one frame, so
there is no random access inside one to offer. An interrupted download is
**restarted**, never resumed, and nothing here sends a `Range` header. What is
proved instead is the `ETag`, which is the file's SHA-256, so re-syncing an
unchanged asset costs a conditional request and no body.

**`current_only`** — a Game Profile carries a revision counter and no
per-version rows. Only `current` can be fetched, what it resolves to changes
when somebody edits the profile, and a build that uses one records a digest it
can check rather than a version it can get back.

### Browse

```console
$ companion aub catalog --scope owned --type map
TYPE  ID               REVISION  NAME  ACCESS
map   z4k7x2m9p1q3w8e  4         e1m1  owned
map   b8n5v2c7x1z9q4w  1         dm3   owned

* the current version of this type cannot be re-fetched later
```

`--scope` is the authorization path being listed, and it is how AUB's catalog is
organised:

| scope | what it lists |
| --- | --- |
| `owned` | your own assets, whatever their visibility |
| `member` | a map you hold a Real-time collaboration role on |
| `workspace` | an asset attached to an Offline Shared Workspace you belong to |
| `public` | somebody else's public asset |

`--all` walks every page; `--type`, `--game` and `--name` narrow it; `--json`
prints the entries verbatim.

### Pin a revision, and sync it

```console
$ companion aub revisions map z4k7x2m9p1q3w8e
REVISION  ID               WHEN                  KIND    DIGEST
4         r9x2k7m4p1q8w3e  2026-09-06T18:22:10Z  upload  9f86d081884c
3         r1v5c8n2z7q4x9w  2026-09-05T09:14:02Z  upload  2c26b46b68ff

2 of 2, retention: retain_all

$ companion aub sync map z4k7x2m9p1q3w8e
synced map z4k7x2m9p1q3w8e at revision 4 (1 fetched, 0 already held, 1483920 bytes)
pin:      r9x2k7m4p1q8w3e
manifest: 0a1b2c3d4e5f6071...
  e1m1.apmap  1483920 bytes  9f86d081884c7d65...
```

The **pin is the revision id `current` resolved to**, never the word `current`.
That is what makes a build repeatable: a save at the backend creates a new
revision beside it, and the pinned one goes on serving the same bytes.

Nothing is published to the cache until its digest **and** its length match what
the server declared, so a tampered, truncated or interrupted transfer leaves the
cache exactly as it was. The record that says "this revision is here" is written
last, after every file has landed — so an interrupted sync leaves objects and no
record, and the next run finds those objects already present and finishes
without re-downloading them.

```console
$ companion aub sync map z4k7x2m9p1q3w8e --revision r1v5c8n2z7q4x9w
synced map z4k7x2m9p1q3w8e at revision 3 (1 fetched, 0 already held, 1402118 bytes)
```

### What is on this machine

`cached`, `verify` and `export` need no network and no session. A revoked
session, an expired token or a flight without wifi takes away new fetches and
nothing else.

```console
$ companion aub cached
TYPE  ID               REVISION  PIN              FILES  BYTES    SYNCED
map   z4k7x2m9p1q3w8e  4         r9x2k7m4p1q8w3e  1      1483920  2026-09-07 11:04
map   z4k7x2m9p1q3w8e  3         r1v5c8n2z7q4x9w  1      1402118  2026-09-07 11:06

$ companion aub verify
ok   map z4k7x2m9p1q3w8e r9x2k7m4p1q8w3e (1 files)
ok   map z4k7x2m9p1q3w8e r1v5c8n2z7q4x9w (1 files)

$ companion aub export map z4k7x2m9p1q3w8e --revision r9x2k7m4p1q8w3e --into ./work
/home/you/work/e1m1.apmap
1 files from map z4k7x2m9p1q3w8e at revision 4 (r9x2k7m4p1q8w3e)
```

`verify` re-hashes every file against its recorded digest, which turns "the file
is there" into "the file is what it was when it was published". A disk that lost
a block is caught here rather than by a compiler producing something strange.

`clean` removes staging files left by interrupted downloads. Nothing else in the
cache is ever removed automatically: it is all re-fetchable, and a build that
pinned a revision names it in its manifest, so anything lost is nameable rather
than merely gone.

The cache lives under your user cache directory, or wherever
`AUCOM_ASSET_CACHE_DIR` points.

### Building from a pinned revision

A build input may name an asset instead of a path:

```console
$ companion build run --pipeline aucom.pipeline.ericw-q1 \
    --input source_map=aub:map/z4k7x2m9p1q3w8e@r9x2k7m4p1q8w3e
```

The syntax is `aub:<type>/<asset_id>[@<revision>][#<file>]`. `@current` is
resolved **before the build starts**, and what the manifest records is the
version it resolved to — never the word `current`. A revision with more than one
file (a prefab package, a texture source with several WADs) must name which one
with `#`, because picking the first would make the answer depend on an ordering
the build did not choose.

The build's `manifest.json` then carries where the input came from, beside the
digest of what it was:

```jsonc
"inputs": [{
  "name": "source_map",
  "sha256": "9f86d081884c7d65…",
  "source": {
    "backend": "http://localhost:9190",
    "asset_type": "map",
    "asset_id": "z4k7x2m9p1q3w8e",
    "revision_id": "r9x2k7m4p1q8w3e",
    "revision": 4,
    "refetchable": true,
    "manifest_sha256": "0a1b2c3d4e5f6071…",
    "fetched_at": "2026-09-07T11:04:12Z"
  }
}]
```

`refetchable` is a real answer either way: `false` means the backend keeps no
per-version row for that asset type, so the build can prove afterwards whether
the source has changed — by comparing the digest — but cannot get the old one
back.

The provenance is deliberately **not** part of `reproducible_key`. The key is
over the recipe, and the same bytes from a different backend are the same build;
a key that included the origin would report "not reproducible" about a build
that reproduced exactly.

A retried build reads its pinned revision out of the local cache, so a newer
revision at the backend cannot change what it compiles.

### When something is refused

```console
$ companion aub sync map z4k7x2m9p1q3w8e
error: /api/companion/v1/capabilities rejected this session. Run `companion auth login --email <address>` and try again.
your locally cached assets are untouched and `companion aub cached` still lists them
```

AUB answers `404` identically for an asset that does not exist and one that is
no longer available to you — a refusal that told the two apart would let anybody
enumerate other people's asset ids one guess at a time — so this program says
both:

```console
$ companion aub show map z4k7x2m9p1q3w8e
error: No asset of this type with this id is available to you.
the asset may have been deleted, or your access to it withdrawn; AUB answers the same way for both
```

A digest that does not match is its own message, and it names the consequence:

```console
error: assetsync: the downloaded bytes are not what the server declared: the bytes hash to 3d4e…, 9f86… was declared
nothing was published to the cache; no build can read these bytes
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
engine   auto-pigeon.engine.darkplaces      1.0.0    builtin
         A heavily extended Quake engine with its own renderer, still the base for several standalone games.
engine   auto-pigeon.engine.fteqw           1.0.0    builtin
         A QuakeWorld-derived engine that also plays NetQuake, with the widest server feature set of the six.
engine   auto-pigeon.engine.ironwail        1.0.0    builtin
         A high-performance QuakeSpasm fork for modern GPUs. Windows and Linux; not macOS.
engine   auto-pigeon.engine.q1-generic      1.0.0    builtin
         The command line every id-derived Quake engine documents, for an engine this build has no profile for.
engine   auto-pigeon.engine.quakespasm      1.0.0    builtin
         The conservative modern Quake port, and what most single-player releases are tested against.
engine   auto-pigeon.engine.quakespasm-spiked 1.0.0    builtin
         QuakeSpasm with FTE's networking bolted on, and the one QuakeSpasm derivative that hosts a dedicated server.
engine   auto-pigeon.engine.vkquake         1.0.0    builtin
         A Vulkan port of QuakeSpasm. Windows, Linux, and macOS through MoltenVK.
tool     auto-pigeon.ericw-tools.q1         1.0.0    builtin
         The qbsp, vis and light map compilers for Quake 1, plus bspinfo and bsputil.
pipeline auto-pigeon.q1.fast-preview        1.0.0    builtin
         The quickest build that is still a playable map: rough visibility, no supersampling.
pipeline auto-pigeon.q1.final               1.0.0    builtin
         Full visibility and 4x supersampled, softened, bounced lighting. Slow on purpose.
pipeline auto-pigeon.q1.normal              1.0.0    builtin
         Compile, full visibility, ordinary lighting with a .lit file. The everyday build.
```

**Qualified means two different things here, and the documents say which.** The
four Q1 tool and pipeline documents are qualified by *measurement*: the version
is the one this workspace's compiler oracle pinned, the download archives are
pinned by digest in the catalogue, and what each program does was established by
running it. The seven engine documents are qualified by *documentation* — their
command lines come from each engine's own published usage, and no build of any
of them has been run by this project. That is not a footnote: every platform in
every one of them is `unverified` with a note saying so, and where upstream
ships nothing it is `unsupported` with the reason. See **Engines**, below.

There was a `sample.q1-toolchain` here, and a `sample.q1-engine`, and writing
the real ones retired both rather than joining them. Two built-in tool profiles
both providing `q1.bsp.compile` would make "which compiler built this" depend on
iteration order; the tool sample described a compiler nobody had run, passing
`-threads` to a `qbsp` that has no such flag; and the engine sample would now be
an eighth engine in a list of seven, describing an engine that does not exist.

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

Five JSON Schema (2020-12) documents are embedded in the binary and are the
contract for anything outside the Companion — an editor, a CI check, a second
implementation:

```console
$ companion profile schema
engine-profile-1.1.schema.json
local-binding-1.1.schema.json
pipeline-profile-1.1.schema.json
profile-common-1.1.schema.json
tool-profile-1.1.schema.json

$ companion profile schema tool-profile-1.1.schema.json > tool.schema.json
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
- **The local binding format is versioned separately** (`aucom.local-binding/1.1`)
  because local state and published documents have different compatibility
  obligations. Tying them together would force a migration of your settings
  every time the published format moved. A build reads every binding version it
  lists — 1.0 and 1.1 today, 1.1 having added the record of which downloads a
  binding depends on — and rewrites a file at the current version the next time
  it saves one.

### Profiles configure independent programs; they do not relicense them

A profile is configuration for software the Auto-Pigeon project did not write
and does not distribute. Describing a program is not distributing it.

- The map-building tools and game engines these profiles drive are **separate
  programs under their own licences**, usually GPL-2.0. They are run as separate
  operating-system processes, exactly as `internal/acquire` requires — never
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

### Publishing a profile, and taking somebody else's

A profile is a document. Auto-Pigeon Backend is where one is **offered to other
people**, **found**, and **fetched by digest**; this program is what decides what
happens on your machine.

#### The export gate: see it before it leaves

```console
$ companion profile preview ./my-toolchain.tool.json
```

The preview is the whole gate. It validates the document, canonicalizes it — one
encoder, so a profile written any other way would be a profile whose digest
depended on who wrote it out — digests it, and then lists **everything it would
make public**: every free-text value, every URL and host, every environment
variable name, and the permissions whoever installs it will be asked to allow.

Nothing is sent. Publishing needs a separate, explicit act:

```console
$ companion profile publish ./my-toolchain.tool.json --visibility=public --confirm
```

**A document that names one machine cannot be previewed at all**, so it cannot be
published: an absolute path, a home directory, a loopback or private address, a
session token, a download credential or an authorization header is a refusal from
the one function that produces the bytes to publish. That is what makes
"publishing a local binding is impossible" a property of the code rather than a
rule somebody has to remember. The backend applies the same rule independently,
because nothing about an HTTP request establishes that its sender is this
program; the two implementations are held to one corpus that both repositories
carry byte-identically.

**Publishing does not change any licence.** A profile is configuration for an
independent program. `license_spdx` is *that program's* licence, and publishing,
installing or running a profile changes nothing about it, does not relicense it,
and grants nothing beyond what its own licence grants.

#### Finding one

```console
$ companion profile catalog --kind=tool --game=quake1 --capability=compile.bsp
$ companion profile published <listing-id>
```

**Compatibility is not endorsement.** A listing reports what its author declared
about platforms, games and capabilities. It is not a recommendation, not a
security review, and not a claim that any of it works.

#### Installing one

```console
$ companion profile install <listing-id>@1.2.0
```

Five checks, in an order that matters, and **nothing is written until you say
so**:

1. the digest is recomputed here over the bytes that arrived, never taken from
   the answer that carried them;
2. those bytes are the document's **canonical** form — the check the backend
   deliberately does not make, because RFC 8785 is this program's algorithm;
3. the document is valid, through the same decoder a pasted file goes through;
4. what changed since whatever is already installed, normalized, and whether it
   asks for **more** than what is installed was granted;
5. what it asks this machine for, in the same words a permission review uses.

Add `--approve` to write the document and record the grant. Without it nothing is
written at all — not even the document — because a document on disk is one the
local catalog lists, and listing something nobody agreed to is how a review
becomes a formality.

**A profile installed from a catalog is `community`, always.** The backend has a
trust state of its own — `community`, `verified`, `builtin` — awarded by that
deployment's operator, and it is shown to you because it is real information. It
never becomes this machine's trust state. `builtin` here means *compiled into this
build of the Companion*, and it is the one state that runs without a grant;
`verified` here means *a catalogue signature this build checked*. Neither happened.
So the badge travels as information, and the authorization stays a grant you made
against one exact digest.

A **withdrawn** version is still installable — reproducing a build that used one
is a legitimate reason to want it — and never silently: the reason its publisher
gave, and any replacement they named, are printed before you approve.

#### Withdrawing one, and reporting one

```console
$ companion profile yank <listing-id> 1.2.0 --reason="It passes -noskip, which corrupts water brushes." \
    --superseded-by=1.2.1
$ companion profile report <listing-id> --category=licence --detail="The source offer is a dead link."
```

A withdrawal keeps the version readable, with its reason, so anybody already
using it is told rather than finding it gone. A reason is required. A report
writes a row the deployment's operator reads and changes nothing about the
listing.

## Acquiring tools

A profile says *how* a tool can be obtained — from `PATH`, from a folder you
point at, from a copy that came with a game, or by a managed download — and
never *where from*. If the download location were in the document, then
withdrawing a compromised build would mean republishing every profile that
pointed at it, and a profile you had already reviewed and granted would keep
pointing at the old bytes. Withdrawal has to be faster than republication, so
the two are separate documents.

Where from is the **acquisition catalogue**: a signed, versioned, revocable map
from a package, a version and a platform to an immutable URL, an exact size, a
SHA-256 digest, a signer, the upstream project, the licence and the
corresponding-source offer.

The catalogue this project publishes is in [`catalog/`](catalog/), and today it
carries one package: `ericw-tools.q1` 0.18.1, for linux/amd64, windows/amd64,
windows/386 and darwin/amd64. There is no arm64 entry on any operating system,
because upstream published no arm64 build — the profile offers `user_path` there
instead, which is the honest answer. Inventing a URL for a download nobody
published would be worse than saying so.

### The four routes

| Mode | What it trusts | What is checked |
| --- | --- | --- |
| `user_path` | you | the files are there, are regular files, and are executable |
| `system_path` | whoever installed it | the same, after a `PATH` lookup of the names the profile declares |
| `already_installed` | the game or package that shipped it | the same, under a root you configured, with no escaping it |
| `managed_download` | the signed catalogue | everything below |

The first three are a *resolution*, not an acquisition: nothing is downloaded,
so there is nothing to verify against, and saying so is better than theatre.
`managed_download` is the only route where the Companion decides what to put on
your disk, and it is the only one with a verification chain.

```console
$ companion acquire resolve qbsp.tool.json --mode system_path
example.qbsp 1.0.0 via system_path
  found on PATH; whoever installed it is who this machine already trusts
  qbsp             /usr/local/bin/qbsp

Nothing was recorded. Add --bind to write this into bindings.json.
```

`--bind` writes the result into `bindings.json`, which is also what records that
a profile depends on a downloaded package — see [Cache cleanup](#cache-cleanup).

### Trust anchors, keyring, catalogue

Three documents, and only the first is not itself signed:

1. **Trust anchors** — Ed25519 public keys this installation accepts as the root
   of the catalogue. They come from a file you install: `AUCOM_CATALOG_ANCHORS`,
   or `"catalog_anchors_path"` in `config.json`.
2. **The keyring**, signed by the anchors. It says which keys may sign a
   catalogue, for how long, and which keys are revoked.
3. **The catalogue**, signed by keys the keyring names.

Two levels rather than one because the two have different lifetimes. A catalogue
changes whenever a tool is published; a keyring changes when a key does. Signing
every catalogue with the anchor would mean the anchor's private key is online,
and an anchor whose private key is online is not something to fall back to.

**No private key is in this repository and none is compiled into this build.** A
Companion with no anchors configured refuses every managed download and says so,
rather than performing one unverified:

```console
$ companion acquire install example.qbsp
error: no catalogue trust anchor is configured: set AUCOM_CATALOG_ANCHORS or the "catalog_anchors_path" field in config.json to a keys file, or use an acquisition mode that does not download

Managed downloads are refused until both are configured. Nothing is downloaded
unverified in the meantime, and the other acquisition modes — a path you choose,
a command on PATH, a copy that came with a game — do not need either.
$ echo $?
1
```

### What verification checks

In order, and all of them, every time:

1. **The payload is exactly its own canonical re-encoding.** A document two JSON
   parsers read differently — a duplicated member, say — is one whose signature
   covers one reading and whose behaviour is the other.
2. **At least one signature is by a key permitted to sign this kind of
   document**, inside its validity window, and not revoked.
3. **The document has not expired.**
4. **Its serial is not lower than the highest this machine has accepted.** A
   correctly signed older catalogue is a replay, not an update.
5. **Nothing it names is revoked** — including by a revocation this machine saw
   once and has remembered ever since.

There is no path from a failure at any of those to a download that happens
anyway, and no flag that turns one off.

### Seeing what a download involves, before it happens

```console
$ companion acquire plan example.qbsp
Example qbsp 1.0.0 (example.qbsp)
  A stand-in for a real map compiler.
  licence:  GPL-2.0-or-later — GNU General Public License v2.0 or later
  terms:    https://www.gnu.org/licenses/old-licenses/gpl-2.0.html
  source for this binary: https://example.invalid/qbsp/source/1.0.0.tar.gz
  project:  https://example.invalid/qbsp
  download: https://catalog.example/example-qbsp-1.0.0-linux-amd64.tar.gz
  platform: linux/amd64, 144 bytes, tar.gz
  digest:   sha256:e2df479f8f3071e5c06d0f75aad4257cee9732279b26aa07fd4dd19c5894677d
  vouched:  key 8a333faed43ed1b9, catalogue example serial 1

  Downloaded programs are separate works, obtained from their own publishers and run as separate processes. They are not part of Auto-Pigeon Companion, are not covered by its MIT licence, and keep their own licence and copyright.
```

Planning fetches and verifies the catalogue and downloads nothing. The URL is
shown with its query string removed: a pre-signed URL's query string *is* a
credential, and it never reaches a log, an error, or the stored install record.

Where a licence requires that a notice be shown before the program is obtained,
the plan carries the notice and the install refuses until you have said you read
it:

```console
$ companion acquire accept example.qbsp
recorded that you were shown the GPL-2.0-or-later notice for example.qbsp 1.0.0

This is a local note that the notice was shown. It is not a licence, it grants you
nothing, and it does not change what the licence requires of anyone.
```

### Installing

```console
$ companion acquire install example.qbsp
installed example.qbsp 1.0.0 for linux/amd64
  digest    sha256:e2df479f8f3071e5c06d0f75aad4257cee9732279b26aa07fd4dd19c5894677d
  vouched   key 8a333faed43ed1b9, catalogue example serial 1
  licence   GPL-2.0-or-later
  tool root ~/.cache/auto-pigeon-companion/tools/entries/sha256-e2df479f…/files/qbsp-1.0.0

Downloaded programs are separate works, obtained from their own publishers and run as separate processes. They are not part of Auto-Pigeon Companion, are not covered by its MIT licence, and keep their own licence and copyright.
```

What happens between those two lines:

- the download lands in a private staging directory, on nobody's `PATH`, under a
  size limit and a ten-minute deadline;
- the exact length is enforced *while* reading, so an unbounded or over-long
  response is stopped rather than truncated;
- the digest is checked before anything is opened;
- an archive is **refused, not sanitized**, if it carries a path that escapes it,
  an absolute path, a symbolic or hard link, a device or a pipe, a setuid bit, a
  duplicated member, or more expansion than it declared. An archive that names
  `../../../.ssh/authorized_keys` meant it; writing it somewhere else would be
  acting on it anyway;
- the whole entry is renamed into place in one operation, so a cache entry never
  exists half-written. Two processes installing the same bytes race for that
  rename and both end up correct.

The cache is content-addressed by the artifact's digest, which is what lets two
pinned versions coexist without colliding and makes a rebuilt release published
under the same version number a *different* entry.

### Verification does not stop at install

The declared executables are re-hashed against the install record every time an
entry is used, and `companion acquire verify` re-hashes everything, including
noticing a file that has *appeared*:

```console
$ companion acquire verify
example.qbsp 1.0.0: 1 file, unchanged since installation
```

A cache lives in a directory your own account can write to, and so does
everything else running as you:

```console
$ companion acquire use example.qbsp
error: acquire: a cached file has changed since it was installed: ~/.cache/…/bin/qbsp is 25 bytes and was installed at 34

The cached copy is not the one that was installed. It has not been replaced
automatically: re-downloading over it would erase the only evidence of what changed.
Remove the entry deliberately, or investigate it first.
```

### Rollback and revocation

The highest serial this machine has accepted, and every revocation it has ever
seen, are kept in `catalog-state.json` — beside `config.json`, **not** in the
cache. The cache is re-downloadable by definition and clearing it should lose
nothing but time; this file is a ratchet, and losing it would restore exactly the
state a replayed old catalogue needs.

```console
$ companion catalog status
trust state ~/.config/auto-pigeon-companion/catalog-state.json
  keyring   example: highest serial accepted 1
  catalogue example: highest serial accepted 2

Serials only go up and revocations are never forgotten. Deleting this file
would restore exactly the state a replayed old catalogue needs.

$ companion acquire plan example.qbsp
error: catalog: rolled back: catalogue example is serial 1 and this machine has already accepted 2; a correctly signed older document is a replay, not an update

This is what a replay of a withdrawn catalogue looks like. It is worth finding out
where the answer came from before doing anything about it.
```

Revocation is **sticky**: a revoked key or a withdrawn artifact is recorded the
first time it is seen and is never forgotten. A later signed document cannot
un-revoke either, because a revocation that a newer document could reverse would
be undone by exactly the party you are revoking against. It is also what makes
revocation reach a tool that is already on disk, and what makes it work with the
network unplugged.

### Offline

`--offline`, or `AUCOM_OFFLINE=1`, forbids every network access. It changes what
is *available*, never what is checked. An install record carries what was
verified and by whom, so using an installed package needs no catalogue at all —
the digest and revocation checks run exactly as they do online. What offline
cannot do is obtain something that is not already there, and it says so.

Catalogue expiry is the one rule that reads differently offline, deliberately: it
bounds how long *new* content may be accepted on a catalogue's word. It is not a
licence that runs out on a compiler you already have. Refusing to run an
already-verified tool because the machine has been off the network for a month
would cost you an afternoon and buy nothing — revocation, which is the mechanism
that actually withdraws something, keeps working.

### Cache cleanup

`companion acquire gc` removes cache entries that **nothing refers to**, and
nothing else. Not "older than", not "over a size budget", not "not the newest
version": every one of those eventually deletes something you deliberately
pinned.

References come from two places, and both matter. A **binding** is a live
dependency — a profile bound to a downloaded toolchain stops working the moment
it is collected — and every version it pins is a reference, which is what
"preserve multiple pinned versions" means in practice. A **job record** is
evidence: it says what ran, and a record whose toolchain has been deleted can no
longer answer the question it was kept for.

```console
$ companion acquire gc --dry-run
Nothing was removed: this was a dry run.

keep    example.qbsp 2.0.0 (sha256:…)
          held by binding example.qbsp
keep    example.qbsp 1.0.0 (sha256:…)
          held by binding example.qbsp
would remove example.other 1.0.0 (sha256:…) — nothing refers to it
```

### Publishing a catalogue

`companion catalog` is the publisher's side. It is in the shipped binary rather
than in a script because a signing procedure that is only a paragraph in a README
stops being true; every rule the verifier enforces is one a publisher has to
satisfy, and both halves are this program's problem.

**Creating the keys.** Do this once. The anchor key belongs offline — on a
machine that does not sign catalogues — and the catalogue key on whatever signs
a release.

```console
$ companion catalog keygen --role anchor --out anchor.key.json
wrote anchor.key.json — anchor key 5d97670e1db855a6, private, mode 0600

the public entry to publish (in an anchor file for an anchor key, in the keyring for a catalogue key):

{
  "key_id": "5d97670e1db855a6",
  "algorithm": "ed25519",
  "public_key": "sl2afg2ZixrvdIxfhq66BcZhGX5gIoTCx2NgDjEBSJo=",
  "role": "anchor",
  "status": "active",
  "not_before": "2026-09-07T00:47:28Z",
  "not_after": "2028-09-07T00:47:28Z"
}
```

A key id is **derived** from the key — the first eight bytes of its SHA-256 — and
never chosen, so a key cannot be published under two names and an entry that
claims one can be checked against the key it carries. `keygen` refuses to
overwrite an existing key file: replacing a signing key is never what anybody
meant.

**Writing the documents.** `keyring.json` names the catalogue keys; `catalog.json`
names the packages. Put the anchor's public entry in an `anchors.json` and the
catalogue key's in the keyring.

Each artifact's `signer` names the key id that vouches for it, and an entry
attributed to a key that did not sign the document it is in is refused. An
artifact that leaves `signer` out means *whoever signs this*: a key id is
derived from the key, so a catalogue kept in a repository cannot know one, and
`catalog sign` fills it in with the signing key. With more than one `--key` it
refuses instead, because "several keys signed this, and one of them vouches for
this entry" is a question the publisher has to answer.

This repository's own catalogue lives in [`catalog/`](catalog/) —
`ericw-tools-q1.catalog.json` pins the four archives upstream published for
ericw-tools v0.18.1, each size and digest measured by downloading the archive
from the URL beside it. It is the payload a person reviews in a pull request;
the signed document is what `catalog sign` makes of it.

**Signing.**

```console
$ companion catalog sign --key anchor.key.json --out pub/keyring.json keyring.unsigned.json
wrote pub/keyring.json — a keyring signed by the anchor key, digest sha256:…
$ companion catalog sign --key catalog.key.json --out pub/catalog.json catalog.unsigned.json
wrote pub/catalog.json — a catalogue signed by the catalog key, digest sha256:…
```

The signed payload is canonical bytes carried as base64, because embedded JSON
does not survive being re-encoded and a signature over a document a proxy
reformatted covers something else. Signing the wrong kind of document with the
wrong key is refused here rather than on somebody else's machine:

```console
$ companion catalog sign --key catalog.key.json keyring.unsigned.json
error: catalog.key.json holds a catalog key, and this document must be signed by the anchor key
```

**Checking it before anyone else does.**

```console
$ companion catalog verify --anchors anchors.json --keyring pub/keyring.json --catalog pub/catalog.json
keyring   example serial 1, expires 2027-01-01T00:00:00Z
          signed by bf94be80e19d6d1d
          keys: 8a333faed43ed1b9
catalogue example serial 1, expires 2027-01-01T00:00:00Z
          signed by 8a333faed43ed1b9
          1 packages, 0 revocations
  example.qbsp 1.0.0 — Example qbsp [GPL-2.0-or-later] for linux/amd64
```

Serve both files, plus the artifacts, under one https address, and point clients
at it with `AUCOM_CATALOG_URL`. The two documents are `keyring.json` and
`catalog.json` under that address.

**Rotating a key.** Publish a keyring with the old key `retired` and the new one
`active`, and a higher serial. Retired means superseded, not compromised:
everything it signed stays signed, it simply stops signing new documents. Keep
both listed for as long as anything might still be verifying an old catalogue.

**Revoking one.** Publish a keyring with the key `revoked`, a `revoked_at` and a
`reason`, and a higher serial. Every client that sees it records the revocation
permanently. To withdraw a *build* rather than a key, add its digest to the
catalogue's `revocations` with a reason and a date, and raise the serial; that
reaches machines that already have it installed, because the revocation applies
on use.

The test keys under `internal/catalog/testdata` are fixtures. They sign nothing
outside `go test`, and there is no production catalogue key in this repository at
all.

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
 "jobs_dir":"/home/you/.cache/auto-pigeon-companion/jobs","aue_available":false,
 "aue_verified":false}

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
    -d '{"profile":"auto-pigeon.engine.quakespasm","action":"play_map",
         "executables":{"engine":"/bin/echo"},
         "roots":{"game_root":"/games/quake","content_root":"/games/quake"},
         "runtime":{"map_name":"level","mod_name":"mymap"}}'
{"schema_version":"aucom.job/1.2","id":"20260906T235240Z-a8b4d547bf25","state":"queued", …

$ curl -s -H "X-AUCOM-Token: $TOKEN" \
    http://127.0.0.1:8791/api/v1/jobs/20260906T235240Z-a8b4d547bf25 | head -c 80
{"schema_version":"aucom.job/1.2","id":"20260906T235240Z-a8b4d547bf25","state":"succeeded",
 "session_role":"client", …

$ curl -s -H "X-AUCOM-Token: $TOKEN" \
    'http://127.0.0.1:8791/api/v1/jobs/20260906T235240Z-a8b4d547bf25/logs'
{"job":"20260906T235240Z-a8b4d547bf25","stream":"stdout","raw":false,
 "summary":{"bytes":52,"stored":52,"dropped":0,"lines":1,"truncated":false,"file":"stdout.log"},
 "text":"-basedir /games/quake -game mymap +map level\n"}

$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/auth/logout
{"authenticated":false}
```

The areas of the page are the same API. Settings, so a machine with no backend
address configured can be given one without editing a file and restarting:

```console
$ curl -s -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/settings
{"aub_base_url":"","port":8791,"job_concurrency":0,"aub_effective_url":"",
 "config_path":"/home/you/.config/auto-pigeon-companion/config.json",
 "tool_cache_dir":"/home/you/.cache/auto-pigeon-companion/tools",
 "jobs_dir":"/home/you/.cache/auto-pigeon-companion/jobs",
 "profiles_dir":"/home/you/.config/auto-pigeon-companion/profiles",
 "builds_dir":"/home/you/.cache/auto-pigeon-companion/builds",
 "asset_cache_dir":"/home/you/.cache/auto-pigeon-companion/assets",
 "bindings_path":"/home/you/.config/auto-pigeon-companion/bindings.json",
 "offline":false,"path_helper":"zenity"}

$ curl -s -X PUT -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/settings \
    -d '{"aub_base_url":"https://aub.example","port":8791,"job_concurrency":0}' | head -c 60
{"aub_base_url":"https://aub.example","port":8791, …

$ curl -s -X PUT -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/settings \
    -d '{"aub_base_url":"aub.example","port":8791,"job_concurrency":0}'
{"error":"\"aub.example\" has no scheme; write it as http://host:port or https://host"}
```

Choosing a path. `pick` opens the desktop's own chooser and returns the one path
the user picked; `validate` checks a typed one the same way. On a machine with no
chooser the pick route says so, by name, and the caller falls back to the text
field rather than to a guess:

```console
$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/paths/pick \
    -d '{"kind":"directory","title":"Where is Quake installed?"}'
{"cancelled":false,"helper":"zenity","path":"/games/quake"}

$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/paths/pick \
    -d '{"kind":"directory"}'          # the user pressed Cancel
{"cancelled":true,"helper":""}

$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/paths/pick \
    -d '{"kind":"directory"}'          # a machine with no chooser installed
{"error":"pathpick: this machine has no file chooser the Companion can open; type the path instead"}

$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/paths/validate \
    -d '{"kind":"directory","path":"quake"}'
{"error":"\"quake\" is not an absolute path; a relative one would depend on where the Companion
 happens to be running","valid":false}
```

The Library. A catalogue read needs a session; the cached list needs neither a
session nor a network, because it is read off this machine's disk:

```console
$ curl -s -H "X-AUCOM-Token: $TOKEN" 'http://127.0.0.1:8791/api/v1/library/catalog?type=map' | head -c 110
{"api_version":"aucom.companion/1.0","scope":"owned","items":[{"asset_type":"map","asset_id":"map-e1m1", …

$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/library/sync \
    -d '{"asset_type":"map","asset_id":"map-e1m1","revision":"rev-000004"}'
{"already_complete":false,"bytes_fetched":2914,"fetched":1,"key":"rev-000004","record":{…},"reused":0}

$ curl -s -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/library/cached | head -c 90
{"items":[{"key":"rev-000004","record":{"schema_version":"aucom.asset-revision/1.0", …
```

Approving a profile, and saying where its program is. The digest is required and
must be the document on disk: an approval for something that has changed since
it was displayed is an approval of something nobody read.

```console
$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" \
    http://127.0.0.1:8791/api/v1/profiles/auto-pigeon.ericw-tools.q1/grant \
    -d '{"digest":"sha256:not-the-one-you-read"}'
{"error":"this approval is for sha256:not-the-one-you-read and the document on this machine is now
 sha256:0a318124692ae01fb58f9ca132edc64e4a57315bb0b6d86c67b1a04ca82d4ef2; read it again before
 approving it"}

$ DIGEST=$(curl -s -H "X-AUCOM-Token: $TOKEN" \
    http://127.0.0.1:8791/api/v1/profiles/auto-pigeon.ericw-tools.q1 | grep -o '"digest":"[^"]*"' | head -1 | cut -d'"' -f4)

$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" \
    http://127.0.0.1:8791/api/v1/profiles/auto-pigeon.ericw-tools.q1/grant \
    -d "{\"digest\":\"$DIGEST\"}" | grep -o '"authorized":[a-z]*'
"authorized":true

$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" \
    http://127.0.0.1:8791/api/v1/profiles/auto-pigeon.engine.quakespasm/bind \
    -d '{"executables":{"engine":"/opt/quakespasm/quakespasm"},
         "roots":{"game_root":"/games/quake","content_root":"/home/you/maps"}}' | head -c 70
{"actions":[…],"authorized":true,"binding":{"executables":{"engine":"/opt/quakespasm/quakespasm"}, …
```

Writing a profile. The wizard's forms post fields; the Companion composes,
validates and digests the document and returns it with a normalized diff against
the template it started from. Nothing is written until you import it, and
importing grants nothing:

```console
$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/profiles/compose \
    -d '{"template":"auto-pigeon.engine.quakespasm","id":"me.engine.my-quake",
         "name":"My Quake build","version":"1.0.0","runtime":"my-quake",
         "engine_version":"1.2.3","executables":{"engine":"my-quake{platform.exe_suffix}"},
         "actions":["play_map","play_package"]}' | head -c 100
{"actions":["play_map","play_package"],"diff":{…},"digest":"sha256:…","document":{…},
 "from":"auto-pigeon.engine.quakespasm","id":"me.engine.my-quake","kind":"engine", …

$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/profiles/compose \
    -d '{"template":"auto-pigeon.engine.quakespasm","actions":["warp_to_hyperspace"]}'
{"error":"this template has no warp_to_hyperspace action; it offers play_map, play_package,
 join_server, host_listen. An action describes something the program actually does, so one has to be
 written rather than named"}

$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/profiles/import \
    -d "{\"document\":$(cat my-quake.engine.json)}" | grep -o '"trust":"[a-z]*"'
"trust":"local"
```

Building. The POST returns as soon as the build has an identity; everything after
that is read back from the manifest on disk, which is why a reload loses nothing:

```console
$ curl -s -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/build/pipelines | head -c 120
{"items":[{"digest":"sha256:5606f106…","id":"auto-pigeon.q1.fast-preview","inputs":[{ …

$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" http://127.0.0.1:8791/api/v1/build/runs \
    -d '{"pipeline":"auto-pigeon.q1.normal","label":"first pass",
         "inputs":{"source_map":"aub:map/map-e1m1@rev-000004#e1m1.map"}}'
{"build":"20260907T134410Z-10b2d970","label":"first pass","pipeline":"auto-pigeon.q1.normal","started":true}

$ curl -s -H "X-AUCOM-Token: $TOKEN" \
    http://127.0.0.1:8791/api/v1/build/runs/20260907T134410Z-10b2d970 | head -c 110
{"live":true,"log":"---- qbsp ----\n","manifest":{"schema_version":"aucom.build/1.1","state":"running", …

$ curl -s -X POST -H "X-AUCOM-Token: $TOKEN" \
    http://127.0.0.1:8791/api/v1/build/runs/20260907T134410Z-10b2d970/cancel
{"build":"20260907T134410Z-10b2d970","cancelled_jobs":["20260907T134411Z-2b1f…"]}
```

A build is cancelled by cancelling the job its current stage *is*, through the
same executor `companion job cancel` uses — so the process tree gets the same
SIGTERM then SIGKILL, the stage records that it was cancelled, and the manifest
is completed rather than abandoned.

| Route | Method | Purpose |
| --- | --- | --- |
| `/` | GET | the embedded frontend, carrying this run's API token |
| `/api/status` | GET | version, platform, AUB address, sign-in state, extractor availability |
| `/api/auth/login` | POST | sign in and persist the session |
| `/api/auth/logout` | POST | forget the session locally |
| `/api/launch-configs` | GET | available launch configurations |
| `/api/launch` | POST | resolve a launch, and submit it as a job unless `dry_run` |
| `/api/aue/version` | GET | the verified extractor's version |
| `/api/v1/jobs` | GET, POST | list jobs; submit one |
| `/api/v1/jobs/preview` | POST | resolve a request into its exact command, start nothing |
| `/api/v1/jobs/{id}` | GET | one job's whole record |
| `/api/v1/jobs/{id}/cancel` | POST | ask it to stop |
| `/api/v1/jobs/{id}/retry` | POST | run its request again, as a new job |
| `/api/v1/jobs/{id}/logs` | GET | `?stream=stdout\|stderr`, `?raw=1` for the bytes the program wrote |
| `/api/v1/jobs/{id}/artifacts` | GET | what it produced |
| `/api/v1/jobs/{id}/artifacts/{name}` | GET | download one |
| `/api/v1/profiles` | GET | what can be run on this machine; `?kind=tool\|engine\|pipeline` |
| `/api/v1/profiles/templates` | GET | the tested documents the wizard starts from |
| `/api/v1/profiles/{id}` | GET | one profile, its permissions, and this machine's binding |
| `/api/v1/profiles/{id}/document` | GET | export: the canonical bytes its digest covers |
| `/api/v1/profiles/validate` | POST | check a document without importing it |
| `/api/v1/profiles/compose` | POST | apply the wizard's fields to a template, validate, digest, diff |
| `/api/v1/profiles/diff` | POST | a normalized diff against the installed document, and whether it escalates |
| `/api/v1/profiles/import` | POST | write a document into the profile directory. Grants nothing |
| `/api/v1/profiles/{id}/bind` | POST | where its programs are here, which roots it may reach, and an approval |
| `/api/v1/profiles/{id}/unbind` | POST | forget this machine's setup. Deletes no files |
| `/api/v1/profiles/{id}/grant` | POST | approve what it asks for, against one exact digest |
| `/api/v1/profiles/{id}/withdraw` | POST | take that approval back. The paths stay |
| `/api/v1/profiles/{id}/remove` | POST | delete an imported document. Built-in ones are refused |
| `/api/v1/engines` | GET | engine profiles, each with its binding and what is stopping it, per action |
| `/api/v1/engines/{id}` | GET | one of them |
| `/api/v1/engines/detect` | GET | game directories that look installed. Proposals; it writes nothing |
| `/api/v1/library/capabilities` | GET | what the backend offers |
| `/api/v1/library/catalog` | GET | the account's assets; `?type=`, `?name=`, `?limit=`, `?cursor=` |
| `/api/v1/library/assets/{type}/{id}` | GET | one asset, its revisions, and which are already here |
| `/api/v1/library/assets/{type}/{id}/{rev}` | GET | one revision with its file list |
| `/api/v1/library/sync` | POST | fetch one exact revision, verifying every file |
| `/api/v1/library/cached` | GET | what this machine holds. No session, no network |
| `/api/v1/build/pipelines` | GET | what can be built, and what is missing when something cannot be |
| `/api/v1/build/preview` | POST | resolve every stage into its exact command, start nothing |
| `/api/v1/build/runs` | GET, POST | past builds; start one |
| `/api/v1/build/runs/{id}` | GET | the manifest, plus the live log while it is running |
| `/api/v1/build/runs/{id}/cancel` | POST | stop the stage that is running |
| `/api/v1/build/runs/{id}/output/{name}` | GET | download something it published |
| `/api/v1/settings` | GET, PUT | the backend address, the port, job concurrency, and where things are |
| `/api/v1/paths/pick` | POST | open the desktop's file chooser and return the one path chosen |
| `/api/v1/paths/validate` | POST | check a typed path the same way |

The `/api/v1` routes are versioned because `companion job` and your own scripts
drive them; the unversioned `/api` routes are the page's own and are not a
contract.

There is deliberately **no route that lists a directory, stats a path or
completes one**. The page is never given the filesystem: it can ask a person a
question in a dialog they see, and receive the answer. Keeping that surface small
is what limits what a mistake behind the guard could cost.

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

CI additionally proves three things about the extractor boundary, because they
are the ones a change could undo quietly: that nothing stages an extractor
binary or embeds one, that no executable is committed anywhere in the tree, and
that the publishing path composes a catalogue package and a compatibility
component from a release manifest and refuses one offering no corresponding
source. It reads **no secrets** — the check that keeps it that way is a job of
its own — because signing a catalogue is a publisher's act performed with a key
that is not in CI, and a workflow that could do it from a pull request would be
a workflow that publishes whatever a pull request contains.

A development build has no extractor installed. Point at a locally built one
instead, and note what it says about itself:

```console
$ AUCOM_AUE_BINARY=../auto-pigeon-extractor/bin/auto-pigeon-extractor \
    ./companion extractor status
extractor: UNVERIFIED developer override
```

## Licence

MIT — see [LICENSE](LICENSE). That covers **this repository's own code only**.

AUE is AGPL-3.0-only and the external map-building tools are GPL — ericw-tools
0.18.1 is GPL-2.0-or-later at the source and GPL-3.0-or-later as the official
binaries are distributed, because they link Embree. All of them are separate
programs, and none is relicensed by anything here.

**No release of this program contains any of them.** They are obtained from
their own publishers, verified against a signed catalogue, and run as separate
processes; the catalogue refuses a copyleft package that offers no
corresponding source, so the offer travels with every one of them.
[THIRD_PARTY_NOTICES.md][notices] sets out what is compiled in, what is run as a
separate process, and what a release redistributes.

[aub]: https://github.com/andrea-dintino/auto-pigeon-backend
[aue]: https://github.com/andrea-dintino/auto-pigeon-extractor
[notices]: THIRD_PARTY_NOTICES.md
