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
- [Testing an editor map for leaks](#testing-an-editor-map-for-leaks)
- [Quake III maps (work in progress)](#quake-iii-maps-work-in-progress)
- [Quake II maps (work in progress)](#quake-ii-maps-work-in-progress)
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

An archive holds the program and the extractor it runs to read maps:

```text
auto-pigeon-companion-1.159-linux-amd64/
  companion                       the Companion (companion.exe on Windows)
  SHA256SUMS                      digests for `sha256sum -c` (not in the macOS archive)
  dependencies/
    auto-pigeon-extractor         the Auto-Pigeon Extractor, a separate program the Companion runs to read maps
    bundle-manifest.json          the SHA-256 of every file; the Companion checks the extractor against it
```

On macOS the archive holds `Auto-Pigeon Companion.app`, with both programs in
`Contents/MacOS/` and the manifest in `Contents/Resources/`.

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

Only one Companion runs at a time. Starting it again opens a new tab of the one
that is running; if that is an older build, it says so, and you must quit it to
use the new one:

```console
$ ./companion
Auto-Pigeon Companion 1.213 is already open at http://127.0.0.1:8789/
This launch (1.214) started nothing; quit the running one first to use this build.
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

**Stopping.** Closing the Companion's last browser tab stops it about 15 seconds
later, so a reload does not. A build, a game or a download it started keeps it
running until that ends, and its window says what it is waiting for. **Quit
Auto-Pigeon Companion…** in the cog menu, or Ctrl+C in the terminal, stops it at
once. `companion --stay-running` keeps it running with no window open.

**Its log.** `companion.log` sits beside its settings
(`%AppData%\auto-pigeon-companion\` on Windows, `~/.config/auto-pigeon-companion/`
on Linux, `~/Library/Application Support/auto-pigeon-companion/` on macOS). It
says which pages connected and why the program stopped, and never holds the
page's access token:

```console
$ grep -E 'lifecycle|shutdown' ~/.config/auto-pigeon-companion/companion.log
```

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

### Setting up the Quake 1 compiler

The qualified compiler is ericw-tools **2.0.0-alpha11** (v0.18.1 is retired).
Download it from its homepage (the profile's **Homepage** button), unpack it,
then in **Profiles › Configure › ericw-tools** choose that folder — the one with
`qbsp` directly inside it. From a terminal, with the profile document from this
repository:

```console
$ companion acquire resolve internal/profile/builtin/ericw-tools-q1.tool.json \
    --mode user_path --user-path ~/tools/ericw-tools-2.0.0-alpha11 --bind
```

A profile only runs after you approve what it does (**Profiles**, or
`companion toolchain grant`).

The Quake 1 compile step passes `-forcegoodtree` by default (**Careful tree
build**, under the advanced options): alpha11's quicker tree build can report a
leak on a map that is sealed. To compare with it off for one build:

```console
$ companion build run --pipeline auto-pigeon.q1.leak-test --input source_map=level.map \
    --option compile.forcegoodtree=false
```

**The Auto-Pigeon build of ericw-tools.** **Profiles** also lists **EricW tools,
Auto-Pigeon build (Quake 1)** and **(Quake II, experimental)**: Auto-Pigeon's own
fork of ericw-tools, published at
<https://github.com/auto-pigeon/ericw-tools/releases> (the profile's
**Homepage** button). It is upstream's 2.0.0-alpha11 plus changes to qbsp that
upstream has not reviewed. The profiles are written for **release 1.4533**; the
profile's description lists what it was measured not to do. Fetch the archive for
your platform, check it against the `SHA256SUMS` beside it, unpack it into a
folder of its own, and choose the unpacked folder — the one that **contains**
`bin/`:

```console
$ companion acquire resolve internal/profile/builtin/ericw-tools-auto-pigeon-q1.tool.json \
    --mode user_path --user-path ~/tools/auto-pigeon-ericw-tools-1.4533-linux-amd64 --bind
$ companion toolchain grant auto-pigeon.ericw-tools.auto-pigeon-build.q1      # read it, then approve
$ companion toolchain check auto-pigeon.ericw-tools.auto-pigeon-build.q1      # asks qbsp, vis and light what they are
$ companion job run --profile auto-pigeon.ericw-tools.auto-pigeon-build.q1 --action compile \
    --input source_map="$PWD/level.map" --root content_root="$PWD" --wait
```

Releases 1.4502 and 1.4505 are refused by the Quake 1 profile (they can fill a
map's air with solid without reporting anything); unpack a newer one. These
profiles are **alternatives**: every built-in build profile keeps naming the
upstream compiler. To build with the Auto-Pigeon one, duplicate a build profile
in **Profiles** and choose it as the tool of each stage.

### A program is asked what it is

A profile can say which program versions it was written for. Pointing the
ericw-tools 2.0.0-alpha11 profile at a 0.18.1 folder makes it **needs setup**,
in Profiles and in every pipeline list, and nothing is compiled. The folder you
picked stays recorded; choose one the profile was written for, or use a profile
written for the version you have. Each program is asked once, as a job you can
see in **Jobs**: when you record its path, when you approve its profile, when it
changed since it was last asked, and when you press **Check these programs**.

```console
$ companion toolchain check auto-pigeon.ericw-tools.q1
light    incompatible       v0.18.1  /opt/ericw-tools-v0.18.1/bin/light
         The light recorded for ericw-tools 2.0.0-alpha11 (Quake 1) is not the program this profile was written for …
$ companion toolchain check auto-pigeon.ericw-tools.q1 --json   # the same answers, with each file's SHA-256
$ companion build pipelines                                     # every pipeline, whether it is ready, and why not
```

```bash
curl -sS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" -X POST \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/profiles/auto-pigeon.ericw-tools.q1/check"
```

A problem's `fault` is `tool_incompatible` (the program said what it is, and it
is the wrong one), `tool_unverified` (nothing was learned: not asked since it
changed, no answer in time, or no version printed) or `tool_unusable` (the file
cannot be run here). Starting a build whose pipeline is not ready is refused
with HTTP 409 and the same reasons, before anything runs:

```bash
curl -sS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  -H 'Content-Type: application/json' -X POST \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/build/runs" \
  -d '{"pipeline":"auto-pigeon.q1.final","inputs":{"source_map":"/path/to/room.map"}}'
# {"class":"pipeline_not_ready","error":"the pipeline auto-pigeon.q1.final needs setup before it can build", "readiness":{…}}
```

**Build & Run keeps the pipeline you chose.** One that stops being ready stays
selected, greyed, with its reason and a link to set it up; nothing is started
with a pipeline you did not choose.

### Building and watching a build

In **Jobs**, a job's name is a link to its page. While a compiler runs, its page
and the **Activity** drawer show what it has printed so far; the full log is
kept with the job:

```console
$ companion job output <job-id> --follow
```

```bash
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/jobs/$JOB_ID/output?source=auto&offset=0"
```

Send the answer's `next` back as `offset` to read on.

**Reading a build back.** Each answer carries the build's manifest and `live` —
whether this Companion is running it right now. Poll until `live` is `false`,
then read `manifest.state`. A build left running by a Companion that stopped is
`interrupted`, and nothing is run again.

```bash
api="$(cat ~/.config/auto-pigeon-companion/api-url)"; token="$(cat ~/.config/auto-pigeon-companion/api-token)"
curl -fsS -H "X-AUCOM-Token: $token" "$api/api/v1/build/runs?limit=5"                # every build, newest first
curl -fsS -H "X-AUCOM-Token: $token" "$api/api/v1/build/runs/<build id>"             # one build
curl -fsS -H "X-AUCOM-Token: $token" -OJ "$api/api/v1/build/runs/<build id>/output/bsp"   # one of its outputs
```

```console
$ companion job show <job id> --json | jq .recovery    # what start-up decided about a job left unfinished
```

**A map file of your own.** **My Maps › On this computer › Choose a map file…**
opens Build on that file. Nothing is uploaded and the file is only read.

**The map's texture WADs.** Build & Run downloads the map's texture bundle and
checks every file against the SHA-256 its manifest declares before a compiler
starts; a bundle that does not match is refused and nothing is built. A WAD is
sent only when the deployment's operator has declared that file redistributable;
its credit and terms are kept in a `NOTICES` folder beside the bundle, and a
run's **Technical details** lists where each WAD came from. When a WAD is **not
sent**, the review says why, and you can name a folder holding your own copy:
only the missing WADs are taken from it, by exact file name.

```bash
# what a map revision's texture bundle carries, and why any WAD was not sent
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/play/textures?asset_id=<map-id>&revision=<n>"
# the same, checking a folder of your own for the WADs that were not sent
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/play/textures?asset_id=<map-id>&revision=<n>&own_wads_dir=/path/to/your/wads"
```

**Auto-build.** Under the map in **Build & Run**, **Auto-build this map** checks
it about every 30 seconds while the Companion runs and builds each newly saved
revision once, with the chosen build profile. It never starts the game. The
revision current when you switch it on is not built; **Build current revision
now** does that. A failed build waits for **Retry**.

```console
$ companion autobuild on <map-id> --pipeline auto-pigeon.q1.normal
$ companion autobuild show <map-id>
$ companion autobuild pipeline <map-id> --pipeline auto-pigeon.q1.final
$ companion autobuild off <map-id>
```

### Pipelines and their arguments

What each program is run with is set on the **stage of the pipeline that runs
it**. Open a pipeline in **Profiles › Configure**: every stage has a list of
extra arguments for that stage alone, each box one argument exactly as typed,
never passed through a shell. **Reset to default** removes them, and the Build
area shows the exact command before anything runs. An argument that repeats a
switch the stage already passes (such as `-threads`) is refused, naming the
parameter to set instead.

```console
$ companion toolchain args auto-pigeon.q1.normal compile --set=-nopercent
$ companion toolchain args auto-pigeon.q1.normal                    # every stage and its arguments
$ companion toolchain args auto-pigeon.q1.normal compile --reset
$ companion toolchain args auto-pigeon.engine.darkplaces engine --set=-window   # an engine keeps its own
```

```bash
curl -fsS -X POST -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  -H 'Content-Type: application/json' \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/profiles/auto-pigeon.q1.normal/stage-arguments" \
  -d '{"stage":"compile","arguments":["-nopercent"]}'
```

### Writing a profile

**Profiles › New profile** is one form in four steps: kind and starting point,
identity, programs and actions (or, for a pipeline, its stages), review and
install. **Start from** a tested profile, or one written on this computer, and
every field is filled in from it for you to change. A pipeline can have any
number of stages; each names the tool that runs it (`"tool"` in the document),
and a stage is never run by another tool instead. Nothing is installed until you
press **Install this profile**, and an installed profile cannot run until you
approve it. To make a new version of a profile of your own, keep its name,
change the version and tick **Replace the installed profile with the same id**.

**Changing a stage's tool keeps what still fits** — inputs, outputs and
parameters the new tool also declares — and says so. Anything the new tool
cannot take opens a review inside the stage, naming each field; nothing changes
until you choose *Keep the current tool* or *Change the tool and revise this
stage*.

**Installed is not ready.** The review ends with what is still to do on this
computer — approve it, say where each program is, set up a stage's tool. A
pipeline whose stages do not fit the installed tools is refused with HTTP 422
and nothing is installed:

```bash
curl -sS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  -H 'Content-Type: application/json' -X POST \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/profiles/compose" \
  -d '{"name":"My build","version":"1.0.0","summary":"One stage.","publisher_name":"Me","license_spdx":"NOASSERTION",
       "scratch":{"kind":"pipeline",
         "inputs":[{"name":"source_map","role":"q1.map.source","required":true,"extensions":[".map"]}],
         "steps":[{"id":"compile","title":"Compile","capability":"q1.bsp.compile","tool":"auto-pigeon.ericw-tools.q1","inputs":{}}]}}'
# → {"valid": false, "error": "this pipeline's stages do not fit the tools installed here: …"}
```

```bash
# a tested profile as the form's fields
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/profiles/templates/auto-pigeon.q1.fast-preview/scratch"
```

A tool profile's diagnostic rules may mark a line `"fatal": true` (the result is
unusable although the program exited 0), give it a `"class"`, and list
`"except"` lines that match the rule but are not its finding.

### Joining and hosting a game

**Live Games** lists games people are hosting. A join link is an
`autopigeon://` URL, and the operating system needs telling that the Companion
opens it:

```console
$ ./companion uri status
scheme:     autopigeon://
registered: yes
$ ./companion uri register
$ ./companion uri unregister
```

The Companion registers itself the first time you start it by double-click (or
with no subcommand), once; a handler you later removed or pointed elsewhere is
left alone. On macOS the `.app` declares it. **Settings › Links from
Auto-Pigeon** does it by hand.

The command is `game open` with **no approval flag at all**: opening a link
checks its shape, shows it in the Companion and starts nothing. Joining is a
separate step you take after reviewing the exact command the page shows.

**Hosting a game.** A hosted game starts **Private**. Choosing **Public** shows
**Help to connect**, which explains the manual UDP port forwarding people
outside your network need to reach you.

## Testing an editor map for leaks

Make sure the `autopigeon://` handler is registered (above), save the map to
your account in the editor and press **Leaks → Test in Companion**. Keep the
editor tab open.

Within about two seconds a notice appears in the open Companion window, with
the map's name and saved revision. **Review leak test** takes that revision and
its game's leak-test pipeline into Build; the preview shows the exact command,
and only **Build** runs the compiler. **Dismiss** forgets that request. The game
is read from the saved map itself, never from the link.

**Which pipeline tests a game's maps is yours to choose, once per game.** The
first request for a game opens **Choose the leak-test pipeline**, listing every
installed pipeline that can test it, the built-in one first. The choice is kept
in `config.json` as `leak_test_pipelines`; change it with **Change pipeline**.

```console
$ ./companion build leak-pipeline --game quake1
quake1 leak tests: nothing pinned; the next request asks which pipeline to use
    auto-pigeon.q1.leak-test  Quake 1 — leak test
$ ./companion build leak-pipeline --game quake1 --pin auto-pigeon.q1.leak-test
$ ./companion build leak-pipeline --game quake1 --unpin
```

```bash
# What is pinned for Quake 1, and what could be.
curl -s -H "X-AUCOM-Token: $(cat "$CONFIG_DIR/api-token")" \
  "http://127.0.0.1:8789/api/v1/leak-test/pipelines?game=quake1"
# Pin one (an empty pipeline unpins).
curl -s -H "X-AUCOM-Token: $(cat "$CONFIG_DIR/api-token")" -H 'Content-Type: application/json' \
  -d '{"game":"quake1","pipeline":"auto-pigeon.q1.leak-test"}' \
  http://127.0.0.1:8789/api/v1/leak-test/pipelines
```

| the saved map is | pipeline offered first | what **Build** runs | point file |
| --- | --- | --- | --- |
| Quake 1 | `auto-pigeon.q1.leak-test` | EricW `qbsp -leaktest` | `.pts` |
| Quake II (experimental) | `auto-pigeon.q2.leak-test` | EricW `qbsp -q2bsp -leaktest` only | `.pts` |
| Quake III | `auto-pigeon.q3.leak-test` | Q3Map2 `-bsp -leaktest` only | `.lin` |
| anything else | none | nothing: the notice says the game has no leak test here | — |

For Quake II, only releases 1.4518, 1.4522, 1.4524 and 1.4533 of the Auto-Pigeon build of
ericw-tools have been measured: make your own copy of the pipeline whose stage
names it (**Profiles**) and pin it for Quake II. For Yamagi Quake II 8.70 on a
64-bit computer, set the light stage's **Lightmap size arithmetic** to `float32`
(1.4522 or newer); without it Yamagi reads some faces' lighting with another row
length:

```bash
./companion build run --pipeline local.pipeline.my-q2-normal --input source_map=room.map \
  --root game_root="$HOME/Quake II/baseq2" --root content_root="$HOME/Quake II/mymod" \
  --option light.lmextents=float32
```

```bash
# What the Companion would run, without running it.
./companion build preview --pipeline auto-pigeon.q3.leak-test \
  --input source_map=aub:map/<map id>@<revision id>
./companion build preview --pipeline auto-pigeon.q2.leak-test --input source_map=room.map \
  --root game_root="$HOME/Quake II/baseq2" --root content_root="$HOME/Quake II/mymod"
```

**Reading the result.** A leak test is judged by what the compiler printed and
left, not by its exit status:

| the result says | it means |
| --- | --- |
| **leak**, with a route | the compiler reached an entity from outside; the route is drawn in the editor |
| **leak**, without a route | it reported a leak but left no usable point file |
| **not tested** | no entity stands in open space (or every one is inside a brush), so nothing was flooded |
| **no leak in this run** | the fill ran and the map compiled — not proof that the map is sealed |
| **no verdict** | an error, a cancel, output cut short, a texture Quake II's qbsp could not open, or an unmeasured compiler version; the reason is given |

A detail brush, a clip brush, a curved patch or a nonsolid surface across a gap
does not seal it.

**Getting the result back to the editor.** The Companion tells your account what
it is doing with the request — received, in review, building, result returned —
and the editor tab shows that, then imports the result if its saved revision
still matches. If the account server is away, the Companion keeps retrying and
nothing is lost; signed out, or signed in as another account, the request waits
until you sign in to the account it belongs to. If the return fails, press
**Retry** (it compiles nothing), or **Download leak result** and import that
JSON file in the editor's Leaks panel.

```bash
# the pending request, and how its status is being delivered
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/leak-test/request"
# the result file of a finished leak-test build
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/leak-test/runs/$BUILD_ID/result" \
  -o "$BUILD_ID-leak-result.json"
```

The result file is a local record of the map revision and the files the build
produced, not a signed attestation.

## Quake III maps (work in progress)

**Setting up Q3Map2.** Bind it once, pointing at a folder where `q3map2` is a
regular file — the Companion refuses a symbolic link, and NetRadiant-custom's
`usr/bin/q3map2` is one: point at a hard link of `q3map2.x86_64` in a sibling
folder of `usr/bin`, so its `../lib` still resolves.

```console
$ companion acquire resolve internal/profile/builtin/q3map2.tool.json \
    --mode user_path --user-path /opt/netradiant-custom/usr/aucom-bin --bind
```

**Building.** `game_root` is the base game's folder and `content_root` your own
content; choose the folder that *contains* `baseq3`. The compiler only sees
`baseq3` and the mod you name, staged for the build — not sibling mods, hidden
folders or links that leave those folders. A map saved to your account brings
the texture packages it is bound to: the Companion downloads them from your
account and checks them by SHA-256, so no content folder is needed.

```console
$ companion build run --pipeline auto-pigeon.q3.fast-preview \
    --input source_map=q3004-room-patch.apmap \
    --root game_root=/opt/quake3 --root content_root=/home/you/q3-project
$ companion build run --pipeline auto-pigeon.q3.fast-preview \
    --input source_map=aub:map/<map id>@<revision id> --root game_root=/opt/quake3
$ companion build run --pipeline auto-pigeon.q3.fast-preview --input source_map=room.map \
    --option compile.mod=missionpack --option vis.mod=missionpack --option light.mod=missionpack
```

**A build that succeeded with warnings is not a finished map.** When a shader
the map names has no image Q3Map2 could find, the BSP is still written and the
build says `succeeded with 1 warning(s): not a complete result`.

**A file the map names is looked for in the game data, never beside the map.**
A terrain's index image (`"alphamap" "hills.pcx"`) must be loose in your game or
content folder's game directory, or in a `.pk3` directly in it; otherwise the
build fails with `map_file_missing`:

```console
$ cp hills.pcx /home/you/q3-project/baseq3/hills.pcx
```

The build's warnings and errors, per stage:

```sh
companion build show <build-id> --json | jq '.steps[] | {id, state, duration_ms, findings: [.diagnostics[]? | select(.severity != "info") | {severity, class, message}]}'
```

**A failure says what kind it is** — `failure_class` on the build, each step and
each job, and `class` in an HTTP refusal:

| class | what it means |
| --- | --- |
| `tool_unavailable` | the compiler is not installed, was moved, or cannot be started |
| `tool_incompatible` | the compiler is not the one its profile was written for; nothing was started |
| `tool_unverified` | the compiler would not say what it is; nothing was started |
| `platform_unsupported` | the tool's profile says it does not run on this platform |
| `game_data_missing` | no folder is set, the folder holds no `baseq3`, a bound package is not held, or the engine found no `pak0.pk3` |
| `archive_damaged` | a PK3 in an approved folder, or a bound package, is not a readable archive |
| `content_refused` | a link out of the approved folders, a same-named different archive, an unreadable package record |
| `fs_game_invalid`, `fs_game_not_found` | the mod directory is not a name, or no folder has it |
| `leak`, `model_missing`, `model_unreadable`, `shader_image_missing`, `map_file_missing` | the compiler's own findings |
| `input_invalid`, `output_invalid`, `output_missing` | a stage's file is not what its role says, or is not there |
| `conversion_refused`, `converter_unavailable` | the extractor would not, or was not there to, write a `.map` |
| `tool_failed`, `timed_out`, `cancelled` | the program's own failure status, its time bound, or you |

**Game data.** `pak0.pk3` is id Software's and is yours to supply; ioquake3
will not start without it in `baseq3`. A free game that is not Quake III keeps
its data in a base directory of another name: build the map with that name as
its mod, and pass `--base-game <name>` when installing.

### Packaging, installing and running a Quake III map

`companion package map` turns one finished build into one archive,
`<game>/auto-pigeon-<map id>-<revision>.pk3`, holding the BSP and only the files
you say may be redistributed. The base game is never packaged. Only a `.pk3`
directly in a game directory is loaded; one in a subfolder is not.

```console
$ companion package map preview --build 20261001T132051Z-07597814
```

The preview lists the archive's members, every source the build read, every
dependency and why it is or is not packaged. A **grant** is your answer to "may
this be redistributed", for one archive (by the SHA-256 `preview` prints), for
the loose files of your content folder, or for one path:

| flag | what you are saying |
| --- | --- |
| `--own-archive <sha256>`, `--own-loose`, `--own-path <path>` | it is your own work |
| `--licensed-archive <sha256>=<licence>`, `--licensed-loose <licence>`, `--licensed-path <path>=<licence>` | you hold a licence that permits redistribution, and you name it |
| `--deny-archive <sha256>`, `--deny-loose`, `--deny-path <path>` | it is not yours to redistribute; it stays out |
| `--include <path>` | also carry this file (a licence text, a level shot); it needs a grant like any other |
| `--accept-missing --reason "<text>"` | write the archive although an engine needs something it will not carry |

`create` refuses (`package_held`) while any file an engine needs is unresolved,
denied or missing:

```console
$ companion package map create --build 20261001T132051Z-07597814 \
    --licensed-archive a7fea0a98f9954644d820b4c920a7d365fccf589b1b5251d156ab00444e5bb02=CC0-1.0 \
    --include LICENSE-apq3011.txt
$ companion package map list
$ companion package map show auto-pigeon-q3011_room-local-498644628db4-9ff2699767f3
```

**Installing.** The default, `--into managed`, does not write into your game
folder: the engine is given a folder the Companion keeps, holding the archive,
the packages the saved map is bound to, and links to your game's files.
`--into game-folder` writes exactly one file into your game folder, never over
another. `--mod <name>` installs into a mod directory. An archive that another
file in the game would override is refused (`load_order_shadowed`).

```console
$ companion package map install auto-pigeon-q3011_room-local-498644628db4-9ff2699767f3 \
    --engine auto-pigeon.engine.ioquake3 --base-game apfree --dry-run
$ companion package map install auto-pigeon-q3011_room-local-498644628db4-9ff2699767f3 \
    --engine auto-pigeon.engine.ioquake3 --base-game apfree
$ companion package map installed
$ companion package map uninstall auto-pigeon-q3011_room-local-498644628db4-managed-f1ba3c83bfde
```

**Running** waits for the engine's own word that the map loaded:

```console
$ companion package map run auto-pigeon-q3011_room-local-498644628db4-managed-f1ba3c83bfde \
    --engine auto-pigeon.engine.ioquake3 --action host_dedicated --option port=27990 --check
map load   ACCEPTED
  The server loaded the map and started its game.
```

`map load` is `ACCEPTED`, `REFUSED` (the engine printed an error or stopped; it
is then stopped, with a class such as `map_not_loaded` or `game_data_missing`),
or `NOT_OBSERVED` (it said neither within `--wait`, and is left running).
`--check` stops the engine once it has answered and returns that answer as the
exit status.

**On the page** the same three steps are the *Package and run this map* panel
under a finished Quake III build in **Build**. Build & Run does not run a Quake
III map. Tested only on Linux x86-64 with Q3Map2 2.5.17n and ioquake3 1.36,
without id Software's data.

## Quake II maps (work in progress)

**Setting up the toolchain.** Both Quake II tool profiles ask qbsp, vis and
light what they are; ericw-tools 0.18.1 has no Quake II and is refused. The
**game folder is the one that holds `pak0.pak`** — `baseq2` itself, not the
folder above it. In Profiles that is the toolchain's **Game directory**; from a
terminal, for a toolchain you wrote with New profile:

```console
$ T=~/.config/auto-pigeon-companion/profiles/<your-q2-tool>.json
$ companion acquire resolve "$T" --mode user_path --user-path "$HOME/tools/auto-pigeon-ericw-tools" \
    --root "game_root=$HOME/Games/Quake II/baseq2" --root "content_root=$HOME/Games/Quake II/baseq2" --bind
$ companion toolchain check <your-q2-tool>
```

**Packaging.** `companion package map` packages one finished Quake II build as
one PAK, `<name>.pak`, holding the BSP and the files of your content folder the
map names and you say may travel with it (textures, sky, sounds, models). What
the base game supplies is never packaged; what nothing supplies is named before
an archive is written. Give an edited copy of an existing map a name of its own.

```console
$ companion package map preview --build 20261014T190432Z-a596fcb7 --own-loose
$ companion package map create  --build 20261014T190432Z-a596fcb7 --own-loose --name q2009room
```

**Installing** writes `<game folder>/aup-<name>/<name>.pak` (or `--mod`), a mod
folder of its own beside `baseq2`, never into it; a failed or cancelled install
leaves the previous one as it was.

```console
$ companion package map install q2-q2009room-38884148033c --engine auto-pigeon.engine.yamagi-quake2
```

**Running** is accepted only when the engine says it opened the map from that
package:

```console
$ companion package map run q2i-aup-q2009room-58887f3869 --engine auto-pigeon.engine.yamagi-quake2 --check
map load   ACCEPTED
opened from …/Quake II/aup-q2009room/q2009room.pak
```

**Setting up Yamagi.** Choose the folder holding its programs (a source build
names the client `quake2`; the Windows release `yquake2.exe`) and, as the game
folder, the folder that holds `baseq2` with `pak0.pak` in it. The game library
and a renderer are looked for beside the program.

**Build & Run plays a saved Quake II map** with one press: choose a Quake II
build profile and a Quake II engine, answer *Your own files in the package*, and
it downloads, converts, compiles, packages, installs and starts the engine. A
missing file, or one nobody said may travel, stops it at *Installing* before
anything is written; *Try again* repeats the request.

```bash
token="$(cat ~/.config/auto-pigeon-companion/api-token)"
api="$(cat ~/.config/auto-pigeon-companion/api-url)"
body='{"asset_id":"<map id>","revision_id":"<revision id>","revision_number":3,
       "pipeline":"auto-pigeon.q2.normal","engine":"auto-pigeon.engine.yamagi-quake2",
       "action":"play_map","map":"myroom",
       "grants":[{"loose":true,"basis":"own_work"}]}'
curl -fsS -H "X-AUCOM-Token: $token" -H 'Content-Type: application/json' -d "$body" "$api/api/v1/play/plan"
curl -fsS -H "X-AUCOM-Token: $token" -H 'Content-Type: application/json' -d "$body" "$api/api/v1/play/runs"
curl -fsS -H "X-AUCOM-Token: $token" "$api/api/v1/play/runs/<run id>" | jq '{state, failed_at, remedy}'
```

**Your account's textures.** A Quake II map saved in Auto-Pigeon can use
textures from a texture source in your account (`aup…/name`), tied to the exact
revision of that source the map was saved with. Build & Run downloads the map's
texture export for the revision you chose and checks every file against its
SHA-256. The compiler is given those `.wal` files at `textures/<source>/<name>.wal`,
in a folder of the build's own made over your content folder. Nothing is written
into your content folder or `baseq2`. The files then go into the package with
where they came from, without asking about your own files. If the map's revision
of a texture source is no longer available, the run stops before compiling and
says which source to update in the editor. Textures that come with the game
(`e1u1/…`) are never sent: the compiler and the engine read them from `baseq2`,
and the package review names any that are not there. A second run of the same
revision uses the export already checked on this computer. Auto-build does the
same for a Quake II map.

```bash
# what the run will download and package for a Quake II map (the "textures" part of the review)
curl -fsS -H "X-AUCOM-Token: $token" -H 'Content-Type: application/json' -d "$body" "$api/api/v1/play/plan" | jq .textures
# after a run: where each texture of the package came from
curl -fsS -H "X-AUCOM-Token: $token" "$api/api/v1/play/runs/<run id>" | jq '.stages[] | select(.state=="downloading_textures") | .detail'
```

Tested only on Linux x86-64 with ericw-tools 1.4518 and Yamagi Quake II 8.70
built from source, with the retail game data.

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
  package targets | preview | create | inspect | verify | extract | map …   build a PAK or PK3 from what a build produced, read one somebody else made, and package, install and run a Quake III or Quake II map
  toolchain validate | show | canonicalize | digest | diff | list | schema | review | grant | withdraw | homepage | args | check  read, check and compare tool, engine and pipeline toolchains, and approve one to run
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
$ companion package map preview --build <build id>   # what a Quake III map's PK3, or a Quake II map's PAK, would hold, and why
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
its terminal, in Settings and in the sign-in dialog.

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
`go` fetches it) and Git. There are no other dependencies.

```console
$ git clone https://github.com/auto-pigeon/auto-pigeon-companion.git
$ cd auto-pigeon-companion
$ go build -ldflags "-X main.version=1.$(git rev-list --count HEAD)" -o companion ./cmd/companion
$ ./companion
```

The binary is CGO-free and cross-compiles:

```console
$ GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o companion.exe ./cmd/companion
$ GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o companion ./cmd/companion
```

A source build has no extractor beside it, so map inspection and conversion are
unavailable until you point it at one you built from the extractor's repository:

```console
$ AUCOM_AUE_BINARY=../auto-pigeon-extractor/bin/auto-pigeon-extractor ./companion extractor status
extractor: UNVERIFIED developer override
$ AUCOM_AUE_BINARY=../auto-pigeon-extractor/bin/auto-pigeon-extractor ./companion extractor convert room.apmap
```

There is no desktop window beyond your browser.

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
