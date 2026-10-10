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

Starting it again while it is open shows the one that is running — a new tab
of it — and starts nothing else, so there is only ever one Companion using
your settings, log and jobs:

```console
$ ./companion
Auto-Pigeon Companion 1.214 is already open at http://127.0.0.1:8789/
```

It names the build that answers. When that is not the one you just started — an
older copy still open — it says so, and nothing of the new one runs until you quit
the open one:

```console
$ ./companion
Auto-Pigeon Companion 1.213 is already open at http://127.0.0.1:8789/
This launch (1.214) started nothing; quit the running one first to use this build.
```

Two starts a moment apart (a double double-click, or a link clicked while the
program is still starting) end with one Companion: the second waits for the
first to finish starting and then shows it.

Closing its last browser tab stops it about 15 seconds later; the wait is so
a reload, or another tab of it, keeps it running. A build, a game or a download
it started keeps it running until that ends, and its window says what it is
waiting for. What it saw is in `companion.log` beside its settings
(`%AppData%\auto-pigeon-companion\` on Windows, `~/.config/auto-pigeon-companion/`
on Linux, `~/Library/Application Support/auto-pigeon-companion/` on macOS):
each page that connected, when and why it went, and the reason it stopped. The
log never holds the page's access token.

```console
$ grep -E 'lifecycle|shutdown' ~/.config/auto-pigeon-companion/companion.log
20:44:11.387 lifecycle: page lease 2 opened (port 55916, Mozilla/5.0 (X11; Linux x86_64) … Chrome/150.0.0.0 Safari/537.36); 2 page(s) open
20:44:16.517 lifecycle: page lease 1 closed after 8.768s: the page closed it (code 1001, going away: a tab closed or navigated); 1 page(s) open
20:44:43.693 lifecycle: page lease 2 closed after 32.307s: the page closed it (code 1001, going away: a tab closed or navigated); 0 page(s) open
20:44:43.693 lifecycle: no page is open; stopping in 15s unless a page opens or work is running
20:44:58.996 lifecycle: stop decided, cause ui_closed; 0 page(s) open
20:44:58.996 shutdown: jobs closed 0s after the decision; printing the exit line and exiting
```

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

**Tools.** For Quake 1 the qualified compiler is ericw-tools **2.0.0-alpha11**
(upstream calls it a pre-release; it is the build Auto-Pigeon's own acceptance
compiles with, and v0.18.1 is retired). Download it from its homepage (the
profile's **Homepage** button), unpack it, then in **Profiles › Configure ›
ericw-tools** choose that folder — the one with `qbsp` directly inside it: the 2.0
release has no `bin/` folder, and an unpacked 0.18.1 is refused naming the
programs it is missing. From a terminal, with the profile document from this
repository:

```console
$ companion acquire resolve internal/profile/builtin/ericw-tools-q1.tool.json \
    --mode user_path --user-path ~/tools/ericw-tools-2.0.0-alpha11 --bind
```

The Quake 1 compile step passes `-forcegoodtree` by default (the **Careful tree
build** option, under the advanced options). 2.0.0-alpha11's quicker tree build can
lose part of a wall and report a leak through solid material on a map that is
sealed; the careful build costs compile time and seals those maps. Every built-in
Quake 1 pipeline, including the editor's **Leaks** test, compiles with it. To
compare with it off for one build:

```console
$ companion build run --pipeline auto-pigeon.q1.leak-test --input source_map=level.map \
    --option compile.forcegoodtree=false
```

A profile only runs after you approve what it does (**Profiles**, or
`companion toolchain grant`).

**The Auto-Pigeon build of ericw-tools.** Beside the upstream compilers,
**Profiles** lists two more tool profiles: **EricW tools, Auto-Pigeon build
(Quake 1)** and **(Quake II, experimental)**. They describe Auto-Pigeon's own
fork of ericw-tools, published at
<https://github.com/auto-pigeon/ericw-tools/releases> — the profile's
**Homepage** button opens that page. It is not an upstream release: it is
upstream's 2.0.0-alpha11 line plus one change to qbsp that closes a clip-hull
leak through thin brushes, and upstream has not reviewed it. The Companion
downloads nothing: fetch the archive for your platform from the releases page,
check it against the `SHA256SUMS` published beside it, unpack it, and choose the
unpacked folder — the one that **contains** `bin/`:

```console
$ companion acquire resolve internal/profile/builtin/ericw-tools-auto-pigeon-q1.tool.json \
    --mode user_path --user-path ~/tools/auto-pigeon-ericw-tools-1.4505-linux-amd64 --bind
$ companion toolchain grant auto-pigeon.ericw-tools.auto-pigeon-build.q1      # read it, then approve
$ companion job run --profile auto-pigeon.ericw-tools.auto-pigeon-build.q1 --action compile \
    --input source_map="$PWD/level.map" --root content_root="$PWD" --wait
# … ---- qbsp / ericw-tools 2.0.0-alpha11-19-g1faae828+auto-pigeon.1.4505 ----
```

These two are **alternatives**, not replacements. Every built-in build profile
keeps naming the upstream compiler, and a stage of your own that names no tool
still resolves to the upstream one, so nothing you already build changes. To
build with the Auto-Pigeon one, duplicate a build profile in **Profiles** and
choose it as the tool of each stage (in a profile document: `"tool":
"auto-pigeon.ericw-tools.auto-pigeon-build.q1"`). The Quake II profile carries
the built-in Quake II profile's steps unchanged; its Quake II mode has not been
measured with this build.

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

**Testing an editor map for leaks.** The Companion tells this computer that it opens
`autopigeon://` links the first time you start it by double-click (or with no
subcommand), and records that it did in `config.json` as `uri_handler`, so it happens
once: a later start leaves a handler you removed, or pointed at another program,
alone. A newer Companion unpacked into another folder takes over the handler an
older one registered. On macOS the `.app` bundle declares it and nothing is written.
`companion serve` (server mode) never registers. To do it by hand: **Settings › Links
from Auto-Pigeon › Open Auto-Pigeon links with this Companion**, or
`./companion uri register`. Then save the map to your account in the editor and press
**Leaks → Test in Companion**. Without that handler the editor's click reaches nothing
and the editor waits for a result that cannot come.

```console
$ ./companion            # first start: the log says "link handler: registered at first use: …"
$ ./companion uri status
```

```console
$ ./companion uri status
scheme:     autopigeon://
registered: yes
```

**Which pipeline tests a game's maps is yours to choose, once per game.** The
Companion ships profiles; the compilers are programs you install yourself, so
nothing is chosen out of the box. The first request for a Quake 1 (or Quake III)
map opens **Choose the leak-test pipeline**: every installed pipeline for that
game that publishes the point file and the compiler's log, the built-in one
first, the ones that need setup greyed with their reasons and **Profiles / setup**
beside them. The choice is kept in `config.json` as `leak_test_pipelines` and can
be changed from the notice (**Change pipeline**).

```console
$ ./companion build leak-pipeline --game quake1
quake1 leak tests: nothing pinned; the next request asks which pipeline to use
    auto-pigeon.q1.leak-test  Quake 1 — leak test
    local.pipeline.new310-win-scratch-q1-build  NEW310 Win Scratch Q1 Build
$ ./companion build leak-pipeline --game quake1 --pin local.pipeline.new310-win-scratch-q1-build
$ ./companion build leak-pipeline --game quake1 --unpin
```

The same over HTTP:

```bash
# What is pinned for Quake 1, and what could be.
curl -s -H "X-AUCOM-Token: $(cat "$CONFIG_DIR/api-token")" \
  "http://127.0.0.1:8789/api/v1/leak-test/pipelines?game=quake1"
# Pin one (an empty pipeline unpins).
curl -s -H "X-AUCOM-Token: $(cat "$CONFIG_DIR/api-token")" -H 'Content-Type: application/json' \
  -d '{"game":"quake1","pipeline":"auto-pigeon.q1.leak-test"}' \
  http://127.0.0.1:8789/api/v1/leak-test/pipelines
```

A request shows as a notice above every area of an open Companion window within
about two seconds, whichever area it is on and without a reload: the map's name,
its saved revision and when it arrived. The Companion checks that the saved revision
and content digest still match, and reads **which game the saved map is for** out of
that pinned revision's own bytes. **Review leak test** takes the pinned revision and
that game's leak-test pipeline into the Build area; the ordinary preview shows the
exact command, and only **Build** runs the compiler. **Dismiss** forgets that request
and no other. Signed out, the notice says so and offers Sign in; a revision that
changed since the click is refused by name.

| the saved map is | pipeline offered first | what **Build** runs | point file |
| --- | --- | --- | --- |
| Quake 1 | `auto-pigeon.q1.leak-test` | EricW `qbsp -leaktest` | `.pts`, from the entity to the outside |
| Quake III | `auto-pigeon.q3.leak-test` | Q3Map2 `-bsp -leaktest` only — no vis, no light, no package, no engine | `.lin`, from the OUTSIDE to the entity |
| anything else (Quake II included) | none | nothing: the notice says the game has no leak test here | — |

The game is never taken from the link, from the page or from a toolbar: a link may
carry a `profile=` hint, and one that disagrees with the saved revision is refused.
A game with no row is not tested as Quake 1.

```bash
# What the Companion would run for a saved Quake III map, without running it.
./companion build preview --pipeline auto-pigeon.q3.leak-test \
  --input source_map=aub:map/<map id>@<revision id>
# … q3map2 -bsp -game quake3 -fs_basepath … -fs_homepath <job workspace> -threads 4 -meta -leaktest -v <staged>.map
```

**A Quake III leak test is read by what Q3Map2 said and left, never by its exit
status.** Measured on Q3Map2 2.5.17n: a leaked map **exits 0**, prints
`Entity N, Brush 0: Entity leaked`, writes `<map>.lin` and writes no BSP. So three
things are recorded apart, and the finished build's line says all three:

| | a leaked Quake III test |
| --- | --- |
| how the process exited | `0` |
| how the compile step ended | `failed` — its BSP is not there |
| what the run says about leaks | `leak`, with a route |

| Q3Map2 printed and left | reading |
| --- | --- |
| `Entity leaked`, a `.lin` | **leak**, with a route |
| `Entity leaked`, no usable `.lin` | **leak**, without a route |
| the `leaked` banner, no entity named, no `.lin` | **not tested** — no entity stands in open space (or every one is inside a brush), so nothing was flooded. Q3Map2 prints `leaked` for that too; it is not a hole |
| the fill, `Writing ….bsp`, the footer, exit 0, the BSP there | **no leak in this run** — not proof the map is sealed |
| an error it stopped on, a cancelled run, output cut short, another Q3Map2 version's clean run | **no verdict** |

A curved patch or a detail brush across a gap does not seal it, and neither does a
brush of a nonsolid shader: Q3Map2 tests structural brushes. A shader it could not
find gets its default flags, and the result says so rather than guessing. Everything
Q3Map2 printed is kept with the build as `compile_log` however the step ended, and
the `.lin` is collected from the job's own fresh directory — never from beside your
map. `companion build pipelines` lists both leak tests.

A leaking map can make qbsp exit with failure and still produce a useful pointfile
and log. The finished build says three things apart: what the compiler found (a
leak route, no leak in that run, or that the test never reached the compiler), how
returning the result to the editor went, and **Download leak result**. A return that
failed has **Retry**, which sends the same result again and compiles nothing.

Keep the original editor tab open. The Companion tells your Auto-Pigeon account what
it is doing with the request — received, in review, which build stage is running,
result returned — and the editor tab shows that line, because a web page is not
allowed to ask a program on your own computer directly. The result itself returns
the same way, and the tab imports it if its saved map revision still matches. If the
return is unavailable, press **Download leak result** and import that JSON file in
the editor's Leaks panel.

That line is delivered, not merely sent. When the account server does not accept a
status — it is away, it is restarting — the Companion keeps the newest thing to say
about that request and sends it again after 1, 2, 4, 8, 16 and then every 30 seconds,
for as long as the request is worth telling anyone about (30 minutes). It sends where
the work **is**: if you reviewed and pressed Build while the server was away, the
editor is told "building", never walked back through "received". While a request is
being worked on its line is repeated every 30 seconds, so a server that restarted and
forgot it has it again. Signed out, or signed in as another account, nothing is sent
and nothing is lost: the same request is acknowledged when you sign in to the account
it belongs to. None of this delays a build, and the notice says so when the editor
has not been told yet:

```bash
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/leak-test/request"
# {"pending":true,"request_id":"…","asset_id":"…","revision":3,
#  "relay":{"state":"retrying","desired":"received","failures":2,
#           "error":"the account server answered HTTP 503"}}
```

`relay.state` is `delivered`, `pending`, `retrying`, `sign_in_required`,
`other_account` or `stopped` (the server rejected the request itself; it is not sent
again). A page asking about one request names it —
`/api/v1/leak-test/pending?request_id=<id>` — and is told `"replaced": true` when a
newer click took its place, instead of being handed the newer request's details.
The result is available through the local guarded API too, for example:

```bash
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/leak-test/runs/$BUILD_ID/result" \
  -o "$BUILD_ID-leak-result.json"
```

Set `BUILD_ID` to the finished leak-test build ID. The URL and token files are
created by the Companion. The JSON records the pinned map identity and artifact
hashes; it is a local result file, not a signed attestation. A Quake 1 result is
`aucom.leak-result/1.0`, unchanged. A Quake III result is `aucom.leak-result/1.1`:
it also names `game_profile`, `compiler`, `pointfile_format` and
`pointfile_direction`, keeps `content_sha256` (the saved revision) apart from
`compiler_source_sha256` (the converted `.map` Q3Map2 read), and carries
`diagnostic` — the reading above, with its evidence. The BSP is never in it.

**Watching a build.** In **Jobs**, a job's name is a link to its page. While a
compiler runs, its page and the **Activity** drawer show what it has printed so
far — from the transcript the tool writes beside its outputs (EricW's
`level.log`, `vis.log`, `light.log`) when it declares one, otherwise its own
output — a stage at a time, bounded, with the full log kept with the job:

```console
$ companion job output <job-id> --follow
```

```bash
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/jobs/$JOB_ID/output?source=auto&offset=0"
```

Send the answer's `next` back as `offset` to read on; `source` names what was read.

**Parameters belong to pipelines.** A build tool says where its programs are and
what they can do; what each one is run with is set on the **stage of the pipeline
that runs it**. Open a pipeline in **Profiles › Configure**: every stage is listed
with the tool that runs it here and a list of extra arguments for that stage alone,
added before its input files — each box one argument exactly as typed, never split
and never passed through a shell. Another pipeline using the same tool does not get
them, and the same tool twice in one pipeline gets two lists. The profile document
is not changed, a profile you export carries none of them, and **Reset to default**
removes them. The Build area's last step shows the exact command before anything
runs, and the job that used them names them. (`-nopercent` is a harmless one
to try.)

```console
$ companion toolchain args auto-pigeon.q1.normal compile --set=-nopercent
$ companion toolchain args auto-pigeon.q1.normal                    # every stage and its arguments
$ companion toolchain args auto-pigeon.q1.normal compile --reset
```

```bash
curl -fsS -X POST -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  -H 'Content-Type: application/json' \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/profiles/auto-pigeon.q1.normal/stage-arguments" \
  -d '{"stage":"compile","arguments":["-nopercent"]}'
```

A build tool takes no new arguments. Ones recorded on a tool before this still
reach every pipeline that runs it, are shown on each such stage, and can be removed
on the tool's page or with `companion toolchain args <tool> <program> --reset`. An
engine is not a stage of a pipeline and keeps its own, as before:

```console
$ companion toolchain args auto-pigeon.engine.darkplaces engine --set=-window
```

**Writing a profile.** **Profiles › New profile** is one form in four steps — kind
and starting point, identity, programs and actions (or, for a pipeline, stages and
their parameters), review and install. **Start from** is *From scratch* by default;
choose a tested profile there and every field of every step is filled in from it,
and stays yours to change, add to or remove. A pipeline can have any number of
stages, each picking a tool, wiring its inputs and carrying its own parameters and
arguments; the same tool may be a stage more than once. A stage names the tool
profile that runs it (`"tool"` in the document), so your own EricW profile and the
built-in one can both be installed: each pipeline runs the one its stages name, and
the built-in pipelines name the built-in tools. Only a stage that names no tool is
refused when two installed tools provide what it needs. A stage whose named tool
cannot run it is refused for that reason — not installed, not a tool, or without
that step — and is never run by another tool instead; nor is it when that tool's
approval is withdrawn. The build record names the tool and digest that ran each
stage. What the form has no field
for is kept as the tested profile had it. Nothing is installed until you press
**Install this profile**, and an installed profile is local and cannot run until you
approve it. **Next** keeps you at the current step when required fields or
server validation fail. Review shows the current validated declaration; installing
opens its approval and setup page. If saving stage arguments fails, setup stays
incomplete and **Retry saving stage arguments** saves them without reinstalling.

**Start from** also lists every profile written on this computer, after the tested
ones and marked *(installed here)* with its version. A form filled from one of those
brings its stages' own arguments with it. Keep its name to make a new version of it —
change the version and tick **Replace the installed profile with the same id**; the
same version can never be rewritten, and the new version needs its own approval. Give
it another name and it is a separate profile, and the one you started from is
untouched.

**Changing a stage's tool compares; it does not reset.** Choosing another tool for a
stage keeps where each input comes from and every parameter the new tool also
declares, and says so under the choice (*Tool changed. Kept: …*). The stage's id,
title, place in the order and its own arguments are never the tool's and are never
touched. The rules, in full:

- an input the new tool declares under the same name keeps its source, provided that
  source produces the kind of file the input takes;
- an input or output the new tool declares under *another* name is carried over only
  when both tools declare exactly one port of that role — the role is the declaration.
  Position is never used, and two candidates are a question for you, not a guess. A
  renamed output repoints the later stages and results that read it;
- a parameter keeps its value when the value passes the new tool's declaration (type,
  range, choices);
- anything else opens a review inside the stage, naming each field, its value and why
  the new tool cannot take it. **Nothing has changed** while it is open, and **Next**
  waits for an answer. *Keep the current tool* leaves the stage exactly as it was.
  *Change the tool and revise this stage* removes what was listed, and the stage then
  says what it still needs. Nothing removed is remembered: choosing the old tool again
  does not bring it back.

A stage that is not complete says so where it is (*This stage is not complete: …*),
and a field holding a value nothing provides any more shows that value, marked, rather
than an empty box.

**Installed is not ready.** The review ends with what is still to do for *this*
document on *this* computer — approve it, say where each program is, set up a stage's
tool — from the same readiness answer Profiles and Build & Run give. A pipeline whose
stages do not fit the tools installed here (an input the tool does not declare, a
required input left unwired, a parameter it does not have) is not valid yet: the form
stays on the stages step, and the import route refuses the same document with HTTP 422
and installs nothing, whoever sends it:

```bash
curl -sS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  -H 'Content-Type: application/json' -X POST \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/profiles/compose" \
  -d '{"name":"My build","version":"1.0.0","summary":"One stage.","publisher_name":"Me","license_spdx":"NOASSERTION",
       "scratch":{"kind":"pipeline",
         "inputs":[{"name":"source_map","role":"q1.map.source","required":true,"extensions":[".map"]}],
         "steps":[{"id":"compile","title":"Compile","capability":"q1.bsp.compile","tool":"auto-pigeon.ericw-tools.q1","inputs":{}}]}}'
# → {"valid": false, "error": "this pipeline's stages do not fit the tools installed here: … does not wire the required input \"source_map\" …"}
```

A valid answer carries `setup`: `{"approved": false, "ready": false, "problems": [...]}`.
A stage whose tool is not installed at all is different — nothing can be said about
its wiring, so the pipeline installs and is listed as needing that tool. A pipeline
starts nothing of its own, so it is ready when every stage's tool is approved and set
up; its own approval is reported beside that, not folded into it.

For example, select **Profiles → New profile → A build tool → From scratch**,
enter its identity, add each program and action, then review and install.
Read its declarations, press **I have read this — approve it**, enter executable
paths and **Save these paths**. Engines also offer their working folder and
declared environment settings; pipelines offer dependency setup and local stage
arguments. Reopen the profile to inspect its readiness before building.

Execution lists keep unavailable profiles and actions visible with their specific
setup reasons, but disable their selection. Under each list, every profile that is
not offered has its own **Set up …** link to its page in Profiles, and **Profiles /
setup** remains available beside these controls. Refresh after completing setup; a valid selection is kept,
and a selection that has lost readiness is cleared.

The server enforces the same readiness: starting a build with a pipeline whose
setup is incomplete — a stale page, a hand-made call — is refused with HTTP 409
before anything is fetched or run, and no failed build is left behind. The answer
carries the same reasons the list shows:

```bash
curl -sS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  -H 'Content-Type: application/json' -X POST \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/build/runs" \
  -d '{"pipeline":"auto-pigeon.q1.final","inputs":{"source_map":"/path/to/room.map"}}'
# {"class":"pipeline_not_ready","error":"the pipeline auto-pigeon.q1.final needs setup before it can build",
#  "readiness":{"ready":false,"problems":[{"fault":"…","summary":"Compile the map: …","fix":"…"}]}}
```

```bash
# a tested profile as the form's fields, and the document composed back from them
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/profiles/templates/auto-pigeon.q1.fast-preview/scratch"
```

**A map file of your own.** **My Maps › On this computer › Choose a map file…**
opens Build on that file, at its map step; choose the build profile and a WAD
folder there as for any file. Nothing is uploaded and the file is only read.

**Build & Run** lists your maps with the most recently saved first. An engine
it calls **Needs setup** says what is missing, in the same words Profiles uses.

**The map's texture WADs.** Build & Run downloads the map's texture bundle from
the Auto-Pigeon server and checks every file in it against the SHA-256 the
bundle's manifest declares before a compiler is started. A bundle that says a
WAD is included and does not carry it, whose bytes or notices do not match their
digests, or that names a path outside its own folder is refused, and nothing is
built.

A WAD installed on the Auto-Pigeon deployment is sent when the deployment's
operator has declared that file's exact bytes redistributable. It is then staged
like any other; you are not asked for a folder. The credit and terms it is sent
under arrive as notice files, which are kept unchanged in a `NOTICES` folder
beside the verified bundle (next to its `LICENSES.md`). A run's **Technical
details** lists, for each WAD, where it came from — installed on the deployment,
an uploaded texture source, carried inside the map, or your own copy — with its
revision when it has one, its SHA-256 and, for a declared installed WAD, its
credit, and says where the notices are.

When a WAD is **not sent**, what is known is that the deployment has no
redistribution permission on record for that file's exact bytes; the review says
so, with the server's reason when it gives one. You can supply your own copy:
name the folder that has it, and only the missing WADs are taken from it, by
their exact file names, each recorded with its SHA-256.

```bash
# what a map revision's texture bundle carries, and why any WAD was not sent
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/play/textures?asset_id=<map-id>&revision=<n>"
# the same, checking a folder of your own for the WADs that were not sent
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/play/textures?asset_id=<map-id>&revision=<n>&own_wads_dir=/path/to/your/wads"
```

Each WAD in the answer has `origin`, `included` and its files' `sha256`; an
installed one also has `redistribution` — `decision` (`included` or `withheld`),
`reason`, and for a carried one its `source`, `credit`, `terms` and
`notice_paths`. `notices` lists the notice files kept with the bundle. Both the
current manifest (`aub-map-texture-export/1.2`) and the previous one (`1.1`,
which never carries an installed WAD and gives no reason) are read.

**Auto-build.** Under the map in **Build & Run**, **Auto-build this map** checks
it about every 30 seconds while the Companion runs and builds each newly saved
revision — once — with the chosen build profile. It never starts the game.
The revision current when you switch it on is not built; **Build current
revision now** does that. A failed build waits for **Retry**.

Switching it off, or choosing another build profile, takes effect at once, even
while the Auto-Pigeon server is slow to answer. An answer that arrives after the
change is not used: nothing is built for a map switched off, and a map switched
off and on again takes a fresh starting point. While a check is waiting for the
server, the panel says **asking the server now** and for how long. A build that
is already running is left to finish.

```console
$ companion autobuild on <map-id> --pipeline auto-pigeon.q1.normal
$ companion autobuild show <map-id>
$ companion autobuild pipeline <map-id> --pipeline auto-pigeon.q1.final
$ companion autobuild off <map-id>
```

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
  job run | preview | list | show | logs | output | cancel | retry | artifacts | profiles   run a profile action as a supervised job, and inspect what ran
  autobuild list | show | on | off | pipeline | build-now | retry   rebuild a hosted map automatically when a new revision is saved (inside a running Companion)
  build run | preview | list | show | pipelines   build a map through a pipeline: several supervised jobs, wired, with a manifest
  package targets | preview | create | inspect | verify | extract | map …   build a PAK or PK3 from what a build produced, read one somebody else made, and package, install and run a Quake III map
  toolchain validate | show | canonicalize | digest | diff | list | schema | review | grant | withdraw | homepage | args  read, check and compare tool, engine and pipeline toolchains, and approve one to run
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
$ companion engine bind auto-pigeon.engine.ioquake3 --executable engine=/opt/ioq3/ioquake3.x86_64 \
    --executable dedicated=/opt/ioq3/ioq3ded.x86_64 --game-root /opt/ioq3   # one flag per program
$ companion package map preview --build <build id>   # what a Quake III map's PK3 would hold, and why
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

By default the Companion uses the server you choose in Settings: **Auto-Pigeon**
or **Auto-Pigeon beta**. A **released** Companion uses any other server — a
local development stack, say — only when the `config.json` beside the executable
names it, or when it is started with `companion --debug` (Settings then accepts
any address):

```console
$ cat config.json      # beside companion(.exe)
{"aub_base_url": "http://192.168.1.20:9190"}
$ ./companion auth status
using AUCOM_AUB_BASE_URL from /opt/auto-pigeon-companion/config.json
aub: http://192.168.1.20:9190
signed in: no
```

That file may hold only `aub_base_url` and `port`. In a release started without
`--debug`, an `AUCOM_AUB_BASE_URL` environment variable, a `.env`, and a
development address saved in Settings are ignored, and the Companion says so on
its terminal, in Settings and in the sign-in dialog:

```console
$ AUCOM_AUB_BASE_URL=http://192.168.1.20:9190 ./companion auth status
ignored server address http://192.168.1.20:9190 from the AUCOM_AUB_BASE_URL environment variable on this computer. A released Companion uses a server other than Auto-Pigeon or Auto-Pigeon beta only when the config.json beside the program names it, or when it is started with --debug.
aub: (not configured — set AUCOM_AUB_BASE_URL or aub_base_url)
signed in: no
```

A development build (from source) reads all of these:

| Variable | Purpose |
| --- | --- |
| `AUCOM_AUB_BASE_URL` | the Auto-Pigeon backend address (a release: only with `--debug`) |
| `AUCOM_PORT` | the port of the local page |
| `AUCOM_PASSWORD` | the password for a scripted `auth login` |
| `AUCOM_OFFLINE` | `1` forbids every network access |
| `AUCOM_JOBS_DIR`, `AUCOM_PROFILES_DIR`, `AUCOM_ASSET_CACHE_DIR` | where jobs, imported profiles and synced assets live |
| `AUCOM_AUE_BINARY` | a development build's extractor instead of the bundled one; shown as **unverified** everywhere |
| `AUCOM_ENV_FILE` | a `.env` to read instead of `./.env` (development only) |

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

A map in your account is an APMap, and a compiler reads a `.map`, so a build
converts it through the extractor first, choosing the extractor's direction by
the APMap's `game`: `quake1` → `--apmap-to-q1map`, `quake2` → `--apmap-to-q2map`,
`quake3` → `--apmap-to-q3map`. A Quake III conversion needs an extractor that
has that direction (Q3_004); it writes the `.map` Q3Map2 compiles and, beside it
in the same `converted-<name>/` directory, `<name>.q3map-manifest.json` — the
source document's identity, every shader, model and sound the map names, and
every conversion warning. An extractor refusal (an older extractor, a patch or
shader it will not write) is reported with its own words and nothing is built.
`extractor convert` runs that same path by hand:

```console
$ AUCOM_AUE_BINARY=../auto-pigeon-extractor/bin/auto-pigeon-extractor \
    ./companion extractor convert q3004-room-patch.apmap
/home/you/maps/converted-q3004-room-patch/q3004-room-patch.map
  sha256:8838724210d17862b8112db69be72ec2226380d12e3b5b72a0e11018e2629e18
  converted by the developer_override extractor  (protocol ), UNVERIFIED
$ ls converted-q3004-room-patch/
q3004-room-patch.map  q3004-room-patch.q3map-manifest.json
```

A `build run` input that is a local `.apmap` goes through the same conversion:
it is copied into the build's temporary stage first, so nothing is ever
written beside your own file. Building the same Quake III map through Q3Map2 —
bound once with `acquire resolve … --mode user_path`, pointing at a directory
where `q3map2` is a regular file (the Companion refuses a symbolic link, and
NetRadiant-custom's `usr/bin/q3map2` is one: point at a hard link of
`q3map2.x86_64` in a sibling of `usr/bin`, so its `../lib` still resolves):

```console
$ companion acquire resolve internal/profile/builtin/q3map2.tool.json \
    --mode user_path --user-path /opt/netradiant-custom/usr/aucom-bin --bind
$ AUCOM_AUE_BINARY=../auto-pigeon-extractor/bin/auto-pigeon-extractor \
    companion build run --pipeline auto-pigeon.q3.fast-preview \
      --input source_map=q3004-room-patch.apmap \
      --root game_root=/opt/quake3 --root content_root=/home/you/q3-project
```

The build manifest records the converted `.map` as the `source_map` input (its
SHA-256 is the extractor's output) and the lit BSP as `bsp`. Playing or hosting
the result needs the base game's `pak0.pk3`, which ioquake3 refuses to start
without and which the Companion does not provide.

**A build that exits 0 with warnings is not a finished map.** Q3Map2 prints
`Couldn't find image for shader textures/…` and still writes the BSP — when
nothing defines the shader (it then compiles with default surface flags), and
also when a shader script defines it but the images the script names are not
there (the script's flags still apply); only a `surfaceparm nodraw` shader never
warns. So the outcome line says so, in the terminal and on the Build page:

```console
$ companion build run --pipeline auto-pigeon.q3.fast-preview --input source_map=room.map \
    --root game_root=/opt/quake3 --root content_root=/home/you/q3-project

build b_… — succeeded with 1 warning(s): not a complete result; read the warnings below
  …
  step bsp      succeeded     412ms  job j_…
    warning  A shader the map names has no image Q3Map2 could find: nothing defines it, or its script names images that are not there. A nodraw shader never warns.
```

The manifest's state is still `succeeded` (the BSP exists and every declared
output was published); the warnings are in each step's `diagnostics`, and
`companion build show <id> --json` carries them.

**A Quake III build reads only what was staged for it.** Q3Map2 is told where
content is with two `-fs_basepath` folders — the base game data (`game_root`) and
your own content (`content_root`) — and, measured on 2.5.17n, it will read
anything it is pointed near: `-fs_game ..` reads the *parent* of each folder, a
mod directory that is not there is initialised without a word, and a truncated
PK3 is skipped in silence with exit status 0. So a build does not hand the
compiler your folders. It stages `<folder>/baseq3` and, when a mod is named,
`<folder>/<mod>` into `<build>/vfs/`, as links to the PK3s that parse as
archives and to the regular loose files, and points Q3Map2 there. A sibling mod,
an engine binary beside `baseq3`, a hidden name such as `.git`, and a link that
leaves the folders you approved are not there to be read. Choose the folder
that *contains* `baseq3`:

```console
$ companion build run --pipeline auto-pigeon.q3.fast-preview \
    --input source_map=q3004-room-patch.apmap \
    --root game_root=/opt/quake3 --root content_root=/home/you/q3-project

build 20261001T101836Z-e30095b2 — succeeded
  input     source_map   sha256:8838724210d17862…
            converted from apmap sha256:3305a41b2c70563c… (q3004-room-patch:full_map, revision 0) by the developer_override extractor, UNVERIFIED
            q3004-room-patch.q3map-manifest.json sha256:8593dae7f39b8cbc… — 4 shader(s), 0 model(s), 0 warning(s)
  game data base game baseq3, fs_game none (a plain base-game map), staged by symlink
            content_root -> …/builds/20261001T101836Z-e30095b2/vfs/content_root
              baseq3/ 1 archive(s), 0 loose file(s), 0 bytes loose
                q3010.pk3                      1432  sha256:634e3c08fff6cb9e…  4 member(s)
            game_root -> …/builds/20261001T101836Z-e30095b2/vfs/game_root
              baseq3/ 0 archive(s), 0 loose file(s), 0 bytes loose
            game_data_missing: the base game directory baseq3 is empty: there is no pak0.pk3 and no loose file, …
```

The mod directory is a **name**, set the same on every stage, and the build
refuses one that is not there instead of compiling without it:

```console
$ companion build run --pipeline auto-pigeon.q3.fast-preview --input source_map=room.map \
    --option compile.mod=missionpack --option vis.mod=missionpack --option light.mod=missionpack
```

What was staged is the manifest's `game_data` (`aucom.build-manifest/1.3`): the
base game and mod names, each archive with its SHA-256 and member count, the
loose file count, what was left out and why. An input that was an APMap also
records, as `inputs[].conversion`, the APMap's digest and its own `document_id`
and revision, and keeps the extractor's `<name>.q3map-manifest.json` beside the
`.map` in the build. This staging belongs to a **build**; `companion job run`
on a single Q3Map2 action uses the bound folders as they are (its `mod` option
still refuses `.` and `..`).

**A build of a saved map stages the packages that map is bound to.** The
editor records a Quake III map's packages in the map itself, by SHA-256
(worldspawn's `auto-pigeon.packages`). When the map comes from your account —
or from a local `.apmap` that carries the same record — the Companion finds
each bound archive in your account *by that digest*, downloads it into its
content-addressed cache through the session it already has, refuses bytes that
do not hash to the binding, and stages each archive under the folder and the
name the map gives. No content folder is needed for such a map, and a package
already fetched builds offline:

```console
$ companion build run --pipeline auto-pigeon.q3.fast-preview \
    --input source_map=aub:map/<map id>@<revision> --root game_root=/opt/quake3
  …
            bound package baseq3/zz_q3010.pk3 sha256:634e3c08fff6cb9e… (1432 bytes, 4 member(s), from the account) — staged
```

A bound package the account does not hold, a download that is not the bound
bytes, and a *different* archive of the same name in your content folder each
stop the build by name. The map's own `mod_root` is the build's mod directory
unless a stage asks for another, which is refused.

**A stage is judged by what it produced and what the compiler said, not by its
exit status.** Three things fail a Quake III stage that exited 0:

| what happened | measured on Q3Map2 2.5.17n | the build |
| --- | --- | --- |
| a required output is not there | a leak with `-leaktest`: no BSP, a `.lin` line file, exit 0 | failed, class `leak`; the `.lin` is published as the `lin` output, downloadable from the page |
| the compiler printed a line its profile marks `fatal` | `ERROR: Unable to open file "models/…"` for a `misc_model`: the BSP is written without it, exit 0 | failed at that stage, class `model_missing`; later stages do not run |
| …or a model that is there and is not one | `ERROR: Invalid MD3 file: Magic bytes not found`, `ERROR: Invalid MD3 header: …`, `ERROR: MD3 File is too small.`: same — BSP without the model, exit 0 | failed at that stage, class `model_unreadable` |
| an output is present and is not what its role says | `-light` given an empty or garbage `.srf` exits 0 and writes a lit BSP | failed, class `output_invalid` or `input_invalid` |

`.bsp` (IBSP 46 with a lump table inside the file), `.prt` (`PRT1` and the
lines its counts promise), `.srf`, `.lin` and the `.map` source are each read
as what they claim to be, on the way into a stage and on the way out. A missing
*image* stays a warning, as above — now with the class `shader_image_missing`.

```console
$ companion build run --pipeline auto-pigeon.q3.fast-preview --input source_map=model.map \
    --root game_root=/opt/quake3 --root content_root=/home/you/q3-project

build 20261001T101838Z-413d8d7a — failed
  error     the compile step: job: q3map2 exited 0 after printing a line its profile marks fatal (model_missing): ERROR: Unable to open file "models/q3010/nothere.md3".
  class     model_missing
```

**A failure says what kind it is.** Beside the sentence, the manifest, each step
and each job carry a `failure_class`, and a refusal that never became a build
carries `class` in the HTTP error:

| class | what it means |
| --- | --- |
| `tool_unavailable` | the compiler is not installed, was moved, or cannot be started |
| `platform_unsupported` | the tool's profile says it does not run on this platform |
| `game_data_missing` | no folder is set, the folder holds no `baseq3`, or a bound package is not held |
| `archive_damaged` | a PK3 in an approved folder, or a bound package, is not a readable archive |
| `content_refused` | a link out of the approved folders, a same-named different archive, an unreadable package record |
| `fs_game_invalid`, `fs_game_not_found` | the mod directory is not a name, or no folder has it |
| `leak`, `model_missing`, `model_unreadable`, `shader_image_missing`, `map_file_missing` | the compiler's own findings, classed by its profile |
| `input_invalid`, `output_invalid`, `output_missing` | a stage's file is not what its role says, or is not there |
| `conversion_refused`, `converter_unavailable` | the extractor would not, or was not there to, write a `.map` |
| `tool_failed`, `timed_out`, `cancelled` | the program's own failure status, its time bound, or you |

**A file the map names is looked for in the game data, never beside the map.**
A terrain entity names its index image by file name (`"alphamap" "hills.pcx"`,
or `_indexmap`). Measured on Q3Map2 2.5.17n with an authored map: it reads that
name at `<game directory>/hills.pcx` — loose in your game folder or content
folder, or inside a `.pk3` directly in the game directory — and nowhere else.
Not beside the `.map`, not in its working directory, not in an archive kept in a
subfolder. So a build does not copy files from beside your map: a map saved to
the account has no folder to copy from, and a file build would otherwise compile
a map the compiler refuses where it lies. When the file is not there the build
fails with the class `map_file_missing`, and the compile stage lists the
compiler's own line under a sentence that says where the file has to be:

```console
$ companion build run --pipeline auto-pigeon.q3.fast-preview --input source_map=terrain.map \
    --root game_root=/opt/quake3 --root content_root=/home/you/q3-project

build 20261009T054512Z-5c1f0e2a — failed
  error     the compile step: job: q3map2 exited with status 1
  class     map_file_missing

$ cp hills.pcx /home/you/q3-project/baseq3/hills.pcx     # the path the key's value gives, under the game directory
$ companion build run --pipeline auto-pigeon.q3.fast-preview --input source_map=terrain.map \
    --root game_root=/opt/quake3 --root content_root=/home/you/q3-project
```

A tool profile declares the two things only its author can know with two
optional members of a diagnostic rule: `"fatal": true` (the line proves the
stage's result is unusable although the program exited 0; requires severity
`error`) and `"class": "<token>"`. On the Build page each stage lists its
findings with the compiler's own line, links to its job, and *What this build
read* shows the conversion, the staged game data and each bound package. For a
map saved to the account it begins with which revision that was and which
`.map` the compiler was handed — the APMap's own `document_id` and revision are
not the account's, and a map saved three times still calls itself revision 0:

```text
source_map: from your account — map q3012_room (ske0lwo5s5mbd0u), account revision 3, revision id k5rlmolcyf33u88
source_map: converted from the map document q3012_room:full_map (revision 0), sha256:987b82a4…
source_map: the .map this build compiled — sha256:626cdf66…
```

The same three facts from the terminal are the manifest's `inputs[].source`
(`asset_id`, `revision`, `revision_id`), `inputs[].conversion.source_sha256` and
`inputs[].sha256`:

```console
$ companion build show <build id> --json | jq '.inputs[] | {source: .source | {asset_id, revision, revision_id}, apmap: .conversion.source_sha256, map: .sha256}'
```

A refusal is shown as its first line — an extractor refusal such as
`apmap-to-q3map refused [q3map_shader_unsafe]: …` leads with the extractor's own
reason — with everything else the program printed behind *Technical details*,
and it is recorded once in Activity, with its class. Reloading the page while a
build runs opens that build again, with its Cancel button; the build itself
never depended on the page, and a reload starts no second compiler.

ioquake3 prints `Quake 3 data files are missing.` and exits 3 when `baseq3`
holds no `pak0.pk3`; the engine job then fails with the class
`game_data_missing` rather than `tool_failed`:

```console
$ companion engine run auto-pigeon.engine.ioquake3 --action host_dedicated --map mymap --mod baseq3
…
job …: failed — did not finish successfully
  exit status 3
$ companion job show <job id> --json | jq -r .failure_class
game_data_missing
```

### Packaging a Quake III map, installing it and running it

A Quake III engine finds everything a map names inside the game directory and
the PK3 archives in it, so a compiled map runs on its author's machine and
nowhere else until its dependencies travel with it. `companion package map`
turns ONE finished Quake III build into ONE archive,
`<game>/auto-pigeon-<map id>-<revision>.pk3`, holding `maps/<name>.bsp` and only
the files you say may be redistributed. The base game is never packaged, and
nothing is inferred from a file's name or from where it was found. Quake and
Quake II packaging is `package preview` / `package create`, unchanged.

**Only an archive directly in a game directory is an archive.** Q3Map2 and the
engines load `<game>/*.pk3` and nothing deeper, so a `.pk3` kept in a subfolder
(`baseq3/tools/x.pk3`) is a file to them: the build stages it as one, the review
does not open it, nothing inside it is a dependency, and the plan's `limits`
name it — *not loaded by the compiler, because it is not directly in the game
directory*. Move it up into the game directory if the map is meant to use it;
building again without moving it changes nothing, and the review no longer asks
for that.

```console
$ companion package map preview --build 20261001T101838Z-413d8d7a --own-loose --json | jq -r '.limits[]'
what is inside baseq3/tools/x.pk3: not loaded by the compiler, because it is not directly in the game directory. …
```

**What the map needs is worked out twice**, because the compiler and an engine
read different things: from the map source (what Q3Map2 looked for) and from the
compiled BSP (what an engine will look for). A `misc_model` is baked into the
BSP, so its `.md3` is a compile dependency and its skin is a runtime one; a
`func_static`'s `model2` is the reverse. A shader's `qer_editorimage` is read by
the compiler only and its stage images by the engine only — measured: Q3Map2
2.5.17n warns when the editor image is absent and says nothing when a stage
image is. So a compile that exited 0 with no warning is not a complete package.

```console
$ companion package map preview --build 20261001T132051Z-07597814
package of build 20261001T132051Z-07597814: the map "q3011_room"
  archive   apfree/auto-pigeon-q3011_room-local-498644628db4.pk3   (per_build)

members, in the order the archive stores them (1, 62836 bytes):
       62836  379775e17c4b  build_output   maps/q3011_room.bsp

sources — what the build's content came from, and what you said about each:
  apfree/zz_apq3011_assets.pk3  sha256 a7fea0a98f9954644d820b4c920a7d365fccf589b1b5251d156ab00444e5bb02
      9 file(s) an engine needs; NO GRANT — nothing from it is packaged

dependencies (8):
  third_party_unresolved R       models/apq3011/beacon.md3
  …
  compile_only           C       models/apq3011/crate.md3
  (C = the compiler reads it, R = an engine reads it)

this package will not be written as it stands (9 reason(s)):
  [rights_unresolved] textures/apq3011/wall.tga (image of textures/apq3011/wall) is not packaged: no grant covers apfree/zz_apq3011_assets.pk3 in your content folder
  …
```

A **grant** is your answer to "may this be redistributed", for one archive (by
its SHA-256, which `preview` prints), for the loose files of your content
folder, or for one path. A grant for a path outranks the one for its archive.

| flag | what you are saying |
| --- | --- |
| `--own-archive <sha256>`, `--own-loose`, `--own-path <path>` | it is your own work |
| `--licensed-archive <sha256>=<licence>`, `--licensed-loose <licence>`, `--licensed-path <path>=<licence>` | you hold a licence that permits redistribution, and you name it |
| `--deny-archive <sha256>`, `--deny-loose`, `--deny-path <path>` | it is not yours to redistribute; it stays out, and the package says so |
| `--include <path>` | also carry this file (a licence text, a level shot); it needs a grant like any other |
| `--accept-missing --reason "<text>"` | write the archive although an engine needs something it will not carry; the reason and the list are recorded in the package |

Each dependency ends as one of: `user_authored` or `licensed` (packaged),
`base_game` (the installed game supplies it; never packaged),
`third_party_unresolved` (nobody has answered), `blocked` (you said no, or its
archive could not be read safely, or its bytes are a known released file),
`missing` (nothing the build read has it), or `compile_only`. `create` refuses
— class `package_held` — while any file an engine needs is unresolved, blocked
or missing:

```console
$ companion package map create --build 20261001T132051Z-07597814 \
    --licensed-archive a7fea0a98f9954644d820b4c920a7d365fccf589b1b5251d156ab00444e5bb02=CC0-1.0 \
    --include LICENSE-apq3011.txt
wrote /home/you/.cache/auto-pigeon-companion/q3-packages/auto-pigeon-q3011_room-local-498644628db4-9ff2699767f3/auto-pigeon-q3011_room-local-498644628db4.pk3
  package   auto-pigeon-q3011_room-local-498644628db4-9ff2699767f3
  sha256    9ff2699767f390774f65f1b7adf3ad171cc625b2d04ecb7b3bb0346fd46a46c6
  members   11, 84969 bytes unpacked, 9765 bytes as an archive
  for       apfree/auto-pigeon-q3011_room-local-498644628db4.pk3
$ companion package map list
$ companion package map show auto-pigeon-q3011_room-local-498644628db4-9ff2699767f3
```

The archive is written by the same bounded writer as every other package here:
members in byte order, fixed timestamps, no path that climbs out, no two paths
that differ only in capitalisation, never over an existing file. Creating the
same package twice produces the same bytes (`--out <dir>` writes it somewhere
of your choosing instead of the package store).

**Installing** puts the archive beside a game. The default, `--into managed`,
does not write into your game folder at all: the engine is given a directory the
Companion keeps, in which the game directory is real and holds the archive plus
a link to each thing in yours. `--into game-folder` writes exactly one file into
your game folder, never over another, and `uninstall` removes it only while it
is still the file that was written.

```console
$ companion package map install auto-pigeon-q3011_room-local-498644628db4-9ff2699767f3 \
    --engine auto-pigeon.engine.ioquake3 --base-game apfree --dry-run
$ companion package map install auto-pigeon-q3011_room-local-498644628db4-9ff2699767f3 \
    --engine auto-pigeon.engine.ioquake3 --base-game apfree
installed …/q3-installs/auto-pigeon-q3011_room-local-498644628db4-managed-f1ba3c83bfde/base/apfree/auto-pigeon-q3011_room-local-498644628db4.pk3
  installation  auto-pigeon-q3011_room-local-498644628db4-managed-f1ba3c83bfde
  target        managed — /home/you/games/freegame is NOT written; its apfree is read through links
  engine reads  fs_basepath …/base, fs_game apfree
  searched before it in apfree: loose files in apfree
$ companion package map installed
$ companion package map uninstall auto-pigeon-q3011_room-local-498644628db4-managed-f1ba3c83bfde
```

Load order is checked before anything is installed. Measured on ioquake3 1.36: a
loose file beats every archive of its game directory, among archives the later
name wins without regard to case, and a mod directory beats the base game — so
an archive named `auto-pigeon-…` loses to `pak0.pk3` and to any `z…` archive
beside it. A map that something else would supply is refused
(`load_order_shadowed`); any other member that would lose is listed.
`--mod <name>` installs into a mod directory instead, where nothing of the base
game is searched first.

**Running** starts the engine and waits for the engine's own word. "The process
started" is not "the map loaded": told to load a map it cannot find, ioquake3
prints `Can't find map`, keeps running and later exits 0.

```console
$ companion package map run auto-pigeon-q3011_room-local-498644628db4-managed-f1ba3c83bfde \
    --engine auto-pigeon.engine.ioquake3 --action host_dedicated --option port=27990 --check
map load   ACCEPTED
  The server loaded the map and started its game.
  the engine said: InitGame: \sv_maxclients\8\…\mapname\q3011_room\…
engine pid 1335921
job        20261001T132150Z-93f4b5e33db6 (cancelled)   `companion job logs 20261001T132150Z-93f4b5e33db6` is the engine's whole output
fs_game    apfree
the engine was stopped
```

`map load` is `ACCEPTED` (the engine printed the line its profile marks
`"signal": "map_loaded"`), `REFUSED` (it printed an error first, or stopped —
the engine is then stopped rather than left idling, with a class such as
`map_not_loaded`, `game_data_missing` or `engine_stopped`), or `NOT_OBSERVED`
(it said neither within `--wait`; it is left running for you to look at — never
a polite word for success). `--check` stops the engine once it has answered and
returns that answer as the exit status; without it the command stays until the
engine exits or you press Ctrl-C.

On the Build page the same run is step 3 of *Package and run this map*. Its
**Port** field belongs to the actions that declare a port (the two servers):
choosing a client action disables it, and a port typed for a server is not sent
with a client run. `GET /api/v1/q3/engines` names each action's declared options
(`"options": ["base_game", "port", …]`), which is what the page reads.

**Game data.** `pak0.pk3` is id Software's and is yours to supply. Measured on
ioquake3 1.36: whenever the base directory is called `baseq3` the engine insists
on it and stops with `Quake 3 data files are missing` — `com_standalone 1` does
not change that. A free game that is not Quake III keeps its data in a base
directory of another name: build the map with that name as its mod, pass
`--base-game <name>` when installing, and the run sends `+set com_basegame
<name>`. In such a folder with only ioquake3's own GPL game code, the dedicated
server loads a packaged map; the windowed client stops with
`Client fatal crashed: Can't load default sound effect …` because Quake III's
client needs data that ships in the game, and the run says so
(`game_data_missing`) instead of reporting a running game.

**On the page** the same three steps are the *Package and run this map* panel
under a finished Quake III build in **Build**: answer for each source, read the
ordered member list and the dependency table, create; choose the engine and the
target and see where it would go; run, and read the pid, the engine's line and
the link to its job. Build & Run does not run a Quake III map and says where
this panel is.

**What has been run where.** Linux x86-64 only: the real Q3Map2 2.5.17n and
ioquake3 1.36, with no id Software data. Windows and macOS compile
(`GOOS=windows`, `GOOS=darwin`) and their unit tests run in CI; no engine has
been started there, and the managed install's links on Windows (a directory
junction where a symbolic link is not permitted, a hard link for a file) have
not been exercised on a Windows machine. No Quake III client session with real
game data has been observed anywhere.

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
head of the branch `build/aue-pin.json` names (the newest extractor at that moment,
resolved to one commit and recorded in the release manifest), puts it beside the
Companion in each
archive, runs each archive on a native runner, and publishes the prerelease.
On each runner the archive's Companion is also driven by a real Chrome or Edge:
two tabs keep it running, closing one does not stop it, and closing the last one
does. The same acceptance runs on your own machine against an archive
(`AUCOM_TEST_BROWSER` names a browser it does not find by itself):

```console
$ build/release-acceptance.py --archive dist/auto-pigeon-companion-1.163-linux-amd64.zip \
    --platform linux-amd64 --version 1.163 --aue-version 1.214 \
    --fixture build/release-fixtures/release-acceptance.apmap
```

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
