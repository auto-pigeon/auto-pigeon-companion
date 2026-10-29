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

**Build tools are set up for you.** The first time the Companion starts, it
installs Auto-Pigeon's own builds of the map compilers — **EricW tools** (Quake 1
and Quake II) and **q3map2** (Quake III) — from their official GitHub releases, in
the background. A **Setting up build tools** card shows each one downloading,
verifying and ready; the page works meanwhile, and nothing builds or starts on
its own when it finishes. The same card says when the Companion is **checking build
tools** (asking each bound program what it is, at start-up or after *Check these
programs*) or **checking the converter**, and if a check fails it says why, with
**Retry** and the full message under Details. On Windows none of this opens a
console window. While it runs, the Companion checks those two releases
about once an hour and **offers** a newer version — it never updates unasked (see
[Build tools: setup and updates](#build-tools-setup-and-updates)).

Those two toolchains are the only programs the Companion downloads. Game engines,
game data and any other tool are ones you install yourself; the Companion finds
them in a folder you choose, on your `PATH`, or in a game's own installation.

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

The page has these areas, in the order of a first run:

| Area | What it is for |
| --- | --- |
| **Build & Run** | One form: a saved map or a file on this computer, a build profile, whether to start the game after the build, a review, and one button. |
| **My Maps** | Your maps in your Auto-Pigeon account, and the revisions this computer has downloaded and verified. |
| **Run** | Starting an engine on its own: joining a server, a mod, a map you already built. |
| **Profiles** | What a tool or engine asks to be allowed to do, approving it, and writing your own. |
| **Jobs** | Everything that has run, with its command, exit status, how long it took and its output. |
| **Settings** | Which Auto-Pigeon you use, and where the Companion keeps things. |

### Build & Run

1. **Map** — **Saved map** (a map and one exact revision from your account) or
   **File on this computer** (a `.map` file and, when it needs one, the folder
   holding its WADs).
2. **Build profile** — the programs that run, and whether they are ready. A
   profile that needs setup says what is missing, with a **Set up** link to the
   tool in Profiles. **Build options** holds the stages and *strict* mode.
3. **Run after build** — on, the engine starts once the map is built; off, the
   build stops there and nothing is installed or started.
4. **Review** — what will be built, with **Technical details** (downloads,
   programs and versions, where files go, the exact command). The button always
   says what it does: **Build & Run**, or **Build** when Run after build is off.
   The Companion checks your choices by itself — **Checking setup…** beside the
   button — and enables it once nothing stops them; when something does, that
   one reason is beside the button with a **Set up** link. One press starts it
   (**Starting…**). Pressing again, or the same build from another tab, follows
   the build already running instead of starting a second. Listing a game for
   everyone, or saying whose files a Quake II package may carry, is asked in a
   dialog first; nothing else is.

The run then has one card with **View progress** (the Activity drawer); if it
fails, the card names the stage, says why in one sentence and offers **Try
again**, which builds the same revision again. Old `#build` links open the form
on **File on this computer**.

```bash
# Build only (Run after build off): the same request, with no engine
api="$(cat ~/.config/auto-pigeon-companion/api-url)"; token="$(cat ~/.config/auto-pigeon-companion/api-token)"
curl -fsS -H "X-AUCOM-Token: $token" -H 'Content-Type: application/json' "$api/api/v1/play/runs" -d '{
  "asset_id": "<map id>", "revision_id": "<revision id>", "revision_number": 4,
  "pipeline": "auto-pigeon.q1.normal", "map": "mymap", "build_only": true}'
```

**Signing in.** **Settings** and the **Sign in** dialog offer the two official
deployments by name — **Auto-Pigeon** (`https://auto-pigeon.com`) and
**Auto-Pigeon beta** (`https://beta.auto-pigeon.com`). Nothing is contacted
until you pick one.

### Build tools: setup and updates

**First launch.** The Companion installs, once, the newest release of each of
`auto-pigeon/ericw-tools` and `auto-pigeon/q3map2` that publishes a build for this
computer, passed its own acceptance there, and is accepted by the built-in
profile. One EricW installation serves Quake 1 and Quake II. Every built-in
profile for those compilers that you have **not** set up yourself is bound to it,
and its programs are asked what they are before any profile reads ready. A
profile you pointed at your own programs is left exactly as it is; Settings
offers **Use the Companion's … for …** beside it.

Where they go: a folder of the Companion's own, one per version, never your
`PATH`, a system folder or a game folder, and no administrator rights:

| System | Tools folder |
| --- | --- |
| Linux | `~/.local/share/auto-pigeon-companion/tools` (or `$XDG_DATA_HOME/…`) |
| macOS | `~/Library/Application Support/auto-pigeon-companion/tools` |
| Windows | `%LocalAppData%\auto-pigeon-companion\tools` |

**What is checked.** Each release's `release-manifest.json` (its schema, tag,
version and the build for this platform), the archive's SHA-256 and size against
it and against GitHub's own digest, and then every file in the archive against
the archive's file list — before anything is unpacked into place and before
anything runs. A SHA-256 published in the same release shows the download is
intact and is the one the release describes; it does **not** show who published
it, and these builds are **not signed**. Offline, a failed setup retries a minute
later and then less often, with a **Retry** button; a working installation keeps
working offline.

**Updates.** About once an hour while the Companion runs (and from **Settings →
Updates → Check for updates**), a newer eligible release is offered in a
dialog naming the tool, the installed and the offered version, and its release
notes:

- **Update now** — downloads and verifies it at once; it is switched on as soon
  as no build is using the version it replaces. A build that is running finishes
  with the programs it started with.
- **Update when I close Companion** — downloads it in the background and switches
  when the Companion closes normally. If the Companion is killed instead, the
  switch happens at the next start, before anything runs.
- **Cancel** — not now. It is offered again at the next start (or a day later).
  Tick **Don't ask again for this version** to skip exactly that version; a newer
  one is still offered, and **Settings → Offer skipped versions again** undoes it.

The previous version is kept for rollback; a version that fails its check is
never switched on. **Every Companion on this computer that uses the same tools
folder waits for the others:** a `companion build run` in a terminal holds an
update chosen in the window exactly as a build in the window does, and each
stage of a build runs the version the build started with. While an update is
waiting to switch, a new build is refused with *"the EricW toolchain is
switching to its new version…"* and nothing of it starts — press it again once
the update is in, or cancel the update; Auto-build simply tries again later. An
older version is never removed while a compiler started from it is still
running, even one whose Companion has since been closed. The same from a
terminal, against the running Companion:

```console
$ companion tools status
Build tools for linux-amd64 (beta channel), in /home/me/.local/share/auto-pigeon-companion/tools
  EricW tools (Auto-Pigeon build)    ready 1.4535
      auto-pigeon-ericw-v1.4535, 2.0.0-alpha11-49-gd645c6f4+auto-pigeon.1.4535, sha256 91cad841…
  q3map2 (Auto-Pigeon build)         ready 2.5.17n-ap.3030
$ companion tools check
$ companion tools update ericw-tools --now          # or --on-close
$ companion tools skip ericw-tools --dont-ask-again
$ companion tools reset-skipped
$ companion tools use-managed auto-pigeon.ericw-tools.q1
$ curl -fsS -H "X-AUCOM-Token: $token" "$api/api/v1/tools" | jq '.tools[] | {id, phase, version, offer: .offer.version}'
$ curl -fsS -H "X-AUCOM-Token: $token" "$api/api/v1/tools" | jq '{checks, check_failures}'   # checks running / failed
$ curl -fsS -X POST -H "X-AUCOM-Token: $token" "$api/api/v1/checks/retry" | jq '.checks'   # Retry the program check
```

The official releases are prereleases today, so the Companion follows the
**beta** channel; `"tools_channel": "stable"` in `config.json` takes only stable
releases. `"managed_tools_off": true` turns setup and checks off (what is
installed stays usable). `companion serve` does none of this unless started with
`--managed-tools on`. No platform without a published, qualified build is
offered one: EricW has no Linux ARM build, and the Companion says so and leaves
setting one up by hand to you.

### Updating the Companion itself

The Companion checks its own releases in the same hourly check, and offers a
newer one in the same dialog, with the same three choices — when two or three
updates arrive together they are one dialog, not three. It never downloads
itself before you choose.

- **Update now** — downloads and checks the new version, then waits until no
  build, conversion, download or game is running. It never stops them. Once
  nothing runs it stops taking new work, restarts on the same address, and your
  open page reloads. A request that arrives in that moment is told to try again;
  nothing of it starts.
- **Update when I close Companion** — downloads and checks it now; it is put in
  place when you close the Companion (the last window, or Quit). Closing one tab
  of several is not closing. It does not reopen the Companion: the next start uses
  the new version.
- **Cancel** — and **Don't ask again for this version**, exactly as for the
  build tools, kept separately from them.

What is replaced is only what the release lists: the program, the extractor
beside it and its bundle manifest. Your settings, profiles, compiler choices,
maps, build history and the `config.json` beside the program are not touched.
The new version is started once, briefly, before it replaces the old one and
again after; if it does not start, the old one is put back and the failure is
shown in **Settings → Updates**. The replaced files are kept in
`.companion-update/previous` beside the program until the next update.

**If an update is interrupted** — the computer loses power, or the Companion is
killed while it swaps its files — the next start puts the previous version back
before anything runs, and never starts a mix of the two versions' files. If that
cannot finish (a file is in use, or the folder became read-only), the Companion
does not start and says so; fix the cause and run the recovery yourself. It
works from the program, or — if the interruption left no program in the folder,
which can happen on Windows — from the copy kept in `.companion-update/previous`:

```console
$ ./auto-pigeon-companion selfupdate recover
The update 1.323 -> 1.324 was undone; 1.323 is complete again in /home/me/Apps/auto-pigeon-companion-1.323-linux-amd64.
$ .companion-update/previous/auto-pigeon-companion selfupdate recover --json | jq .outcome
```

No further update is installed, and nothing kept for recovery is removed, until
the last one is finished.

A Companion in a folder you cannot write to, and the macOS app, are not replaced
by the Companion: the update is downloaded and checked, and Settings says where
it is and what to replace. A development build is never updated automatically;
Settings names the newest release for you to download.

```console
$ companion tools status
  …
  Auto-Pigeon Companion              ready 1.318
      update offered: 1.318 → 1.320 (https://github.com/auto-pigeon/auto-pigeon-companion/releases/tag/v1.320)
$ companion tools update companion --now          # or --on-close
$ companion tools cancel-pending companion
$ companion selfupdate status
Auto-Pigeon Companion 1.320 (linux-amd64), /home/me/Apps/auto-pigeon-companion-1.318-linux-amd64
  updated automatically when you choose an update: its own files are replaced, nothing else in the folder
  bundled extractor 1.245
  last update: 1.318 -> 1.320, verified 2026-10-28T23:10:04+01:00
```

### Setting up the Quake 1 compiler

Normally there is nothing to do: the Companion installs the Auto-Pigeon build
(above). To use another ericw-tools instead:

The qualified compiler is ericw-tools **2.0.0-alpha11** (v0.18.1 is retired).
Download it from its homepage (the profile's **Homepage** button), unpack it,
then in **Profiles** open **ericw-tools** and, under **Setup**, choose that
folder — the one with `qbsp` directly inside it. From a terminal, with the
profile document from this repository:

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
upstream has not reviewed. The profiles are written for **release 1.4535**; the
profile's description lists what it was measured not to do. Fetch the archive for
your platform, check it against the `SHA256SUMS` beside it, unpack it into a
folder of its own, and choose the unpacked folder — the one that **contains**
`bin/`:

```console
$ companion acquire resolve internal/profile/builtin/ericw-tools-auto-pigeon-q1.tool.json \
    --mode user_path --user-path ~/tools/auto-pigeon-ericw-tools-1.4535-linux-amd64 --bind
$ companion toolchain grant auto-pigeon.ericw-tools.auto-pigeon-build.q1      # read it, then approve
$ companion toolchain check auto-pigeon.ericw-tools.auto-pigeon-build.q1      # asks qbsp, vis and light what they are
$ companion job run --profile auto-pigeon.ericw-tools.auto-pigeon-build.q1 --action compile \
    --input source_map="$PWD/level.map" --root content_root="$PWD" --wait
```

Releases 1.4502 and 1.4505 are refused by the Quake 1 profile (they can fill a
map's air with solid without reporting anything); unpack a newer one. These
profiles are **alternatives**: every built-in build profile keeps naming the
upstream compiler. To build with the Auto-Pigeon one, duplicate a build profile
in **Profiles** and choose it as the tool of each stage. When the Companion set
the tools up itself, every built-in build profile already runs the Auto-Pigeon
build, because the upstream-named profiles are bound to it too.

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
with a pipeline you did not choose. The others that need setup are listed as
`— needs setup` and summed up once, in the closed **Other pipelines (n need
setup)** list under the select: what each still needs, a **Set up** link to the
profile to fix (the tool, not the pipeline), and the exact words under
**Details**. Engine and action lists work the same way.

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

**While a tool runs** its page and the Activity card show one line, refreshed
every second: the stage, the tool's own progress when it prints one (EricW's
VIS says *Base VIS* then *Full VIS*, each with its own percentage — Full VIS can
be quiet for a long time), its CPU and memory, and how long ago it last printed,
for example `VIS · Full VIS 37% · CPU 285% · RAM 640 MiB · last output 4 s ago`.
CPU counts 100% per busy logical CPU, so four busy threads read about 400%. RAM
is the resident memory of the tool's own processes (on Windows, the tool's own
process only; on macOS it is not measured). *Running; no output for 20 s* means
just that — quiet, not stuck. The output view can follow the end or stay where
you are reading (**Jump to latest**), wrap long lines, copy, and download the
whole log.

```bash
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/jobs/$JOB_ID" | jq .live
# {"now": …, "supervised": true, "last_output_at": …, "progress": {"phase": "Full VIS", "percent": 37},
#  "resources": {"state": "live", "scope": "process_group", "cpu_percent": 285.2, "rss_bytes": 671088640, …}}
```

**Playing a map's latest build.** On a **My Maps** card, **Play latest build**
appears when this computer has a finished, normal build of that map whose level
is still exactly as built. It opens **Run** with that build chosen; nothing is
compiled, and nothing starts until you press **Start**. The card says which
revision it was built from, and *Built from revision N; newer changes are not
included.* when you have saved since — **Build latest** then builds the newer
one. A leak test is a diagnostic, never a playable build: a map with only leak
tests says *No playable build yet: only leak tests.* What counts is how each
build was made — an editor's leak request, or a built-in leak-test pipeline —
never which pipeline is pinned for leak tests today: pinning your normal pipeline for leak tests does not hide its
normal builds, before or after.

```console
$ companion build latest --map <map id>              # the build Play latest build would play
20261028T140035Z-698c5fa9  revision 4  quake1  Q1 normal  finished 2026-10-28T14:01:12Z
$ companion engine stage --game-root ~/quake --mod auto-pigeon \
    --build "$(companion build latest --map <map id> --game quake1 --id-only)"
```

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
opens Build & Run on that file. Nothing is uploaded and the file is only read.

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

**Which engines Build & Run offers.** An engine is offered only for a build
profile of its own game — each profile says which game it is for — so a Quake 1
build never offers a Quake II or Quake III engine, whatever its name. Choosing a
build profile for another game clears an engine that cannot play it and says why;
your saved setup is not touched. The page names the game's own folder (`id1`,
`baseq2`, `baseq3`) from the engine you chose, and never writes into it.

```bash
# which engines can play what each build profile makes
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/build/pipelines" | jq '.items[] | {id, engine_fit}'
```

**An engine that cannot start.** Build & Run watches the game for its first few
seconds. An engine that stops at once — a library it cannot find, an argument it
refuses — ends the run **Failed** at *Starting the game*, naming the engine's job
in **Jobs**, where its own output says why.

**An engine's project folder.** When you save a Quake or Quake II engine's
program and game folder in **Profiles** and leave **Content folder** empty, the
Companion fills it with its own folder — where it keeps the maps and textures it
downloads — and creates it. The field shows the folder; type another and save to
change it. A Quake III engine reads its packages from that folder, so you choose
it yourself. Profiles says **Partly ready** and names each action that still needs
something, rather than **Ready**, when only some of what an engine does can start.

```console
$ companion engine bind auto-pigeon.engine.vkquake --engine /opt/vkquake/vkquake --game-root ~/Games/Quake
recorded for auto-pigeon.engine.vkquake:
  engine         /opt/vkquake/vkquake
  content_root   /home/you/.cache/auto-pigeon-companion/assets
  game_root      /home/you/Games/Quake
```

**When the server refuses or does not answer.** Build & Run says which it was,
in one sentence, with what fixes it: **Sign in** when your sign-in expired; a
review of the changed Terms or notices on the Auto-Pigeon site (the Companion
never accepts them for you), then **Try again**; **Try again** when the server
is away. A texture download the server could not serve is said as exactly
that, and nothing is compiled. The server's own answer is one click away. A map
file on this computer still builds meanwhile.

**Automatic builds.** Under the build profile, **Automatically build new saved
revisions** checks the saved map every 30 seconds while the Companion runs and
builds each newly saved revision once, with the build profile chosen above. It
never starts the game, and the revision saved when you switch it on is not
built. It can be switched on only with a ready profile; if that profile later
needs setup, automatic builds wait and say what to repair. Choosing another
profile while it is on asks **Apply to automatic builds**. Unchecking it, or
**Stop automatic builds**, stops what comes next, never a build already running.
A revision you built by hand is not built again automatically.

```console
$ companion autobuild on <map-id> --pipeline auto-pigeon.q1.normal
$ companion autobuild show <map-id>
$ companion autobuild pipeline <map-id> --pipeline auto-pigeon.q1.final
$ companion autobuild off <map-id>
```

### Profiles

**Profiles** lists every profile with its kind, its game and one state:
**Ready**, **Partly ready** (or *Ready: Play a map only* when one action can
start), **Needs setup**, **Needs approval**, **Incompatible program** or
**Unavailable on this platform**. Search by name, kind or game; open a profile
by its name. Its page reads top to bottom:

1. **Setup** — what is missing, by program, and the folder or program pickers.
   A pipeline names the tool to set up instead, with a **Set up** link to it.
   *Using:* says the version the programs reported, which may be a compatible
   release other than the one the profile is named for.
2. **Options** — what the document sets for each program, and under
   **Advanced options** each program's own extra arguments.
3. **Approval** — for a profile that did not ship with the Companion.
4. **Details** (closed) — description, publisher and licence, trust, each
   program's exact path, version and digest, **Check these programs**, the
   homepage, the exact commands, and removing a profile of your own.

```bash
# the state the Profiles list shows for one profile
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/profiles/auto-pigeon.q1.normal" | jq .setup_state
# → {"state": "needs_setup"}
```

### Pipelines and their arguments

What each program is run with is set on the **stage of the pipeline that runs
it**. Open a pipeline in **Profiles** and its **Advanced options**: every stage
has a list of extra arguments for that program alone, each box one argument
exactly as typed, never passed through a shell — a path with spaces is one box.
**Clear** empties one list on the page; nothing changes until **Save
arguments**, which is offered only while there is something to save. An
argument that repeats a switch the stage already passes (such as `-threads` or
qbsp's `-nopercent`) is refused beside that program, naming the parameter to set
instead. **Details** shows each stage's exact command, the same words the build
runs.

```console
$ companion toolchain args auto-pigeon.q1.normal compile --set=-nocolor
$ companion toolchain args auto-pigeon.q1.normal                    # every stage and its arguments
$ companion toolchain args auto-pigeon.q1.normal compile --reset
$ companion toolchain args auto-pigeon.engine.darkplaces engine --set=-window   # an engine keeps its own
```

```bash
curl -fsS -X POST -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  -H 'Content-Type: application/json' \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/profiles/auto-pigeon.q1.normal/stage-arguments" \
  -d '{"stage":"compile","arguments":["-nocolor"]}'

# each stage's exact command as it would run on this machine
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/profiles/auto-pigeon.q1.normal/commands"
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
Changing the kind clears only programs and stages; the name and game stay. The
document itself is under **Advanced: the document itself** — **Open a file…**
reads one you pick, **Download as a file** saves it through your browser, and
going back to the form from a document you edited asks first.

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

## Building a map from the editor

Make sure the `autopigeon://` handler is registered (above), save the map to
your account in the editor and press **Build** (or **Build & Run**) there. The
editor opens a link that names only an opaque request:

```console
$ ./companion game open 'autopigeon://build/request/<id>?intent=build' --no-browser
the Companion is open at http://127.0.0.1:<port>/#play
```

Opening it starts nothing and asks nobody anything. A notice — **Build requested
by the editor** or **Build & Run requested by the editor** — appears in the open
Companion window. The Companion asks your account, with its own sign-in, which
saved map and revision the request is about, and checks that revision itself.
**Review build** opens Build & Run on that exact saved revision, with Run after
build set as the editor asked, and checks the setup as usual; nothing runs until
you press **Build** or **Build & Run**. If a compiler is not set up yet, **Set up**
beside the button takes you there, and the request waits (up to thirty minutes).
**Dismiss** forgets the request and tells the editor.

The editor sees what happens — received, in review, building, then finished,
failed or cancelled. If you change the map, the revision or Run after build, the
build is no longer the editor's: it still runs when you press the button, and
the editor is not told about it. A request for another account or server, or an
expired one, says the Companion cannot find it; sign in to the account the
editor uses and press **Retry**. Quake III maps are not built this way yet.

The Companion that picks the request up also tells the editor **which Companion
it is**: its version, the editor-handoff protocol it speaks, what it can do
(build, Build & Run, leak test) and, when the link started a newer copy that
handed the request to one already running, that copy's version too. The editor
uses it to say "received by Companion 1.330", to tell you when the Companion that
answered cannot do what was asked, and — when nothing answers — that nothing did.
Nothing is sent before you are signed in, and it never starts a build. Leak tests
(below) are acknowledged the same way. An older Companion sends none; the editor
then says the version is unknown.

```bash
# the pending request, from this machine only
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/build-request/request"
# what your account says it is about
curl -fsS -H "X-AUCOM-Token: $(cat ~/.config/auto-pigeon-companion/api-token)" \
  "$(cat ~/.config/auto-pigeon-companion/api-url)/api/v1/build-request/pending"
```

## Testing an editor map for leaks

Make sure the `autopigeon://` handler is registered (above), save the map to
your account in the editor and press **Leaks → Test in Companion**. Keep the
editor tab open.

Within about two seconds a notice appears in the open Companion window, with
the map's name and saved revision. **Review leak test** takes that revision and
its game's leak-test pipeline into Build; the preview shows the exact command,
and only **Build** runs the compiler. **Dismiss** forgets that request. The game
is read from the saved map itself, never from the link.

**A Quake 1 leak test compiles with the map's own textures.** When you press
**Build**, the Companion takes the texture bundle of exactly the saved revision
the editor asked about — from its verified cache, or downloaded and verified
from your account, the same bundle Build & Run uses — and gives the compiler
its own copy (`-wadpath`), removed when the build ends. The build log says
which bundle it was. A texture folder typed on the Build page is not used for
an editor's request, and nothing is looked for elsewhere on this computer: when
the textures cannot be fetched (signed out, refused, a download that does not
verify), nothing runs, the request stays open, and **Build** can be pressed
again. A bundle your account marks incomplete is compiled as it is, and the
compiler's warnings name the WAD or texture that was missing. The manifest
records which bundle the build used:

```sh
$ ./companion build show <build-id> --json |
    jq '.roots[] | select(.role == "content_root") | .source | {kind, bundle: (.bundle | {map_id, revision, digest, compiler_ready})}'
```

**Which pipeline tests a game's maps is yours to choose, once per game.** The
first request for a game opens **Choose a leak-test pipeline**, listing every
installed pipeline that can test it, the built-in one first. Pick a ready one —
the dialog shows it as **Ready** with its own summary — and press
**Use pipeline**; the review in Build follows, and nothing runs until you press
**Build** there. **Cancel**, `×` and Escape choose nothing and keep the request.
Pipelines that need setup are listed disabled, and what each one still needs is
in the closed **Other pipelines (n need setup)** list under it: one line per
missing program set, a **Set up** link to the tool's own page in Profiles, and
the exact reasons under **Details**. The choice is kept in `config.json` as
`leak_test_pipelines`; change it with **Change pipeline**.

```console
$ ./companion build leak-pipeline --game quake1
quake1 leak tests: nothing pinned; the next request asks which pipeline to use
    auto-pigeon.q1.leak-test  Quake 1 — leak test  (runs compile)
$ ./companion build leak-pipeline --game quake1 --pin auto-pigeon.q1.leak-test
$ ./companion build leak-pipeline --game quake1 --unpin
```

```bash
# What is pinned for Quake 1, and what could be.
curl -s -H "X-AUCOM-Token: $(cat "$CONFIG_DIR/api-token")" \
  "http://127.0.0.1:8789/api/v1/leak-test/pipelines?game=quake1"
# Why each one that is not ready is not: every problem names the tool profile,
# the stage and the program it is about — the profile is where it is set up.
curl -s -H "X-AUCOM-Token: $(cat "$CONFIG_DIR/api-token")" \
  "http://127.0.0.1:8789/api/v1/leak-test/pipelines?game=quake1" |
  jq -r '.choices[] | select(.readiness.ready | not) | .name as $n
         | .readiness.problems[] | "\($n): set up \(.profile) (\(.step): \(.executable))"'
# Pin one (an empty pipeline unpins).
curl -s -H "X-AUCOM-Token: $(cat "$CONFIG_DIR/api-token")" -H 'Content-Type: application/json' \
  -d '{"game":"quake1","pipeline":"auto-pigeon.q1.leak-test"}' \
  http://127.0.0.1:8789/api/v1/leak-test/pipelines
```

| the saved map is | pipeline offered first | what **Build** runs | point file |
| --- | --- | --- | --- |
| Quake 1 | `auto-pigeon.q1.leak-test` | EricW `qbsp -leaktest` | `.pts` |
| Quake II (experimental) | `auto-pigeon.q2.leak-test` | EricW `qbsp -q2bsp -leaktest` only | `.pts` |
| Quake III | `auto-pigeon.q3.leak-test`, then `auto-pigeon.q3.leak-test.auto-pigeon-build` | Q3Map2 `-bsp -leaktest` only (upstream's, or the Auto-Pigeon build's) | `.lin` |
| anything else | none | nothing: the notice says the game has no leak test here | — |

**Every choice says what it runs.** The dialog shows each pipeline's stages, and
the notice of a request names the pinned pipeline and its stages before you
review it: a pipeline you made may run visibility and lighting too, after the
leak test's compile stage (they run only when the map is sealed). While it
builds, the editor is told which stage is running and every stage the build
runs (`vis of compile,vis,light`), so it never promises a leak test alone for a
build that does more.

```bash
# The stages of every Quake III choice, built-in ones first.
curl -s -H "X-AUCOM-Token: $(cat "$CONFIG_DIR/api-token")" \
  "http://127.0.0.1:8789/api/v1/leak-test/pipelines?game=quake3" |
  jq -r '.choices[] | "\(.id) builtin=\(.builtin): \([.steps[].id] | join(", "))"'
```

For Quake III with the Auto-Pigeon build of Q3Map2 set up in **Profiles**, pin
the built-in `auto-pigeon.q3.leak-test.auto-pigeon-build` — the same one-stage
leak test, run by that tool; no stage to retarget and no binding to edit:

```console
$ ./companion build leak-pipeline --game quake3 --pin auto-pigeon.q3.leak-test.auto-pigeon-build
```

For Quake II, only releases 1.4518, 1.4522, 1.4524, 1.4533 and 1.4535 of the Auto-Pigeon build of
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

A Quake II build stops when its BSP is one a Quake II engine cannot run: more
brushes, brush sides, planes, nodes, leaves or texture infos than Yamagi Quake II
8.70 loads, a visibility lump over 1 MiB, a brush side whose plane index the
engine reads as a negative number (so the player would walk through or into the
wrong walls), or a map qbsp could only write in Qbism's extended format. The job
fails at the stage that wrote the file and says which limit and what to change —
usually fewer, larger pieces, or a split map. If your engine reads Qbism BSPs,
choose that format for the compile stage:

```bash
./companion build run --pipeline local.pipeline.my-q2-normal --input source_map=big.map \
  --root game_root="$HOME/Quake II/baseq2" --root content_root="$HOME/Quake II/mymod" \
  --option compile.format=qbism
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

**The Auto-Pigeon build of Q3Map2.** A second profile,
`auto-pigeon.q3map2.auto-pigeon-build`, describes the Auto-Pigeon fork of q3map2
published at <https://github.com/auto-pigeon/q3map2/releases> (the profile's
**Homepage** button): upstream's compiler plus fixes for thin walls and rods
that lost their collision, faces beside a near-parallel face that were not
drawn, a thin sliver that could fill a pipe's bore, and narrow real openings a
slot 0.125 wide through a wall that upstream reports sealed. It is written for
**release 2.5.17n-ap.3030**, and its description lists what that release was
measured to do and not to do. Fetch the archive for your platform, check it
against the `SHA256SUMS` beside it, unpack it into a folder of its own, and
choose the unpacked folder — the one that **contains** `bin/`, where `q3map2`
sits beside every library it loads:

```console
$ companion acquire resolve internal/profile/builtin/q3map2-auto-pigeon.tool.json \
    --mode user_path --user-path ~/tools/auto-pigeon-q3map2-2.5.17n-ap.3030-linux-amd64 --bind
$ companion toolchain grant auto-pigeon.q3map2.auto-pigeon-build      # read it, then approve
$ companion toolchain check auto-pigeon.q3map2.auto-pigeon-build      # asks q3map2 what it is
$ companion job run --profile auto-pigeon.q3map2.auto-pigeon-build --action compile \
    --input source_map="$PWD/room.map" --root game_root=/opt/quake3 --root content_root="$PWD" --wait
```

Release 2.5.17n-ap.3017 is refused (it reads past a table on a degenerate brush
side); unpack a newer one. Like the EricW build, this profile is an
**alternative**: every built-in Quake III build profile keeps naming the upstream
compiler. To build with the Auto-Pigeon one, duplicate a build profile in
**Profiles** and choose it as the tool of each stage. Its leak test is built in:
`auto-pigeon.q3.leak-test.auto-pigeon-build` (above). A build records, for each
program it started, the SHA-256 of the file and what that same file said it was
when it was last checked — beside the version the profile expects:

```sh
companion build show <build-id> --json | jq '.tools[] | {expected: .tool_version, ran: [.executables[] | {sha256, identity, identity_state}]}'
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
build says `succeeded with 1 warning(s): not a complete result`. A compiler's
line about a tool texture — ericw-tools 2.0 prints `unable to find texture skip`
for every Quake 1 map — is kept in the build's findings as information, not as a
warning: `skip`, `clip`, `hint` and the other tool textures have no picture in
any WAD, so they are never missing. Any other texture that was not found is still
a warning:

```sh
$ ./companion build show <build-id> --json | jq '[.steps[].diagnostics[] | {severity, raw}]'
```

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
under a finished Quake III build of a file on this computer in **Build & Run**.
Build & Run does not run a Quake III map itself. Tested only on Linux x86-64 with Q3Map2 2.5.17n and ioquake3 1.36,
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
  serve [--port <n>] [--open] [--debug] [--interactive | --stay-running] [--managed-tools auto|on|off]   run the local GUI server; it keeps running until stopped unless --interactive
  auth login [--email <address>] | status | logout   authenticate against auto-pigeon-backend
  aub capabilities | catalog | show | revisions | sync | cached | verify | export | clean   browse auto-pigeon-backend's assets and sync exact revisions to this machine
  job run | preview | list | show | logs | output | cancel | retry | artifacts | profiles   run a profile action as a supervised job, and inspect what ran
  autobuild list | show | on | off | pipeline | build-now | retry   rebuild a hosted map automatically when a new revision is saved (inside a running Companion)
  tools status | check | retry | update | skip | cancel-pending | reset-skipped | use-managed   the build tools the Companion installs and updates itself: EricW tools and q3map2 (inside a running Companion)
  selfupdate status | prove --expect-version <v> | recover   this Companion's own installation, how its last update ended, a new version's proving start, and finishing an interrupted update
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

**A map from Auto-Pigeon cannot make the compiler read your files.** A map's text
can name files the compiler then opens: worldspawn's `wad`/`_wad` list, and a
`misc_external_map`'s `_external_map`. For every input that came from
Auto-Pigeon — Build & Run, hosting, a leak test — the Companion confines the copy
it compiles before any step reads it (`NEW_348`): a WAD entry that is absolute,
has a drive, or climbs out with `..` keeps only its file name (which is how the
map's WADs are laid out for the build), and an `_external_map` stops the build
by name. A map file you choose on your own disk is compiled exactly as it is.

```text
Error: Compiling: the input "source_map": the map names an external map file
(`_external_map`), which a map from Auto-Pigeon may not do: the compiler would
read that file from this computer. Remove the misc_external_map entity, or merge
its brushes into the map
```

**The local page answers only your own account.** The page at
`http://127.0.0.1:<port>/` carries this run's API token so it can work, and
loopback is reachable by every account on the computer. The Companion asks the
operating system which account opened each connection and refuses one opened by
another account, on every route (`NEW_348`) — on Linux from `/proc/net/tcp` and
`/proc/net/tcp6`, on Windows from the IPv4 and IPv6 connection tables and the
process token, and on macOS from the kernel's socket list (the one `netstat`
reads, `net.inet.tcp.pcblist_n`; `NEW_353A` — before that macOS served every
connection unasked). A connection whose owner the system could not name is
refused too (`NEW_349`): a client can connect from an IPv6 socket to
`::ffff:127.0.0.1`, and that must not be a way past the check. A web page on
another site could never read the token; this closes the other account on a
shared computer. Nothing about starting the Companion changes: the browser your
own account opens is your account.

```console
$ sudo -u someone-else curl -s -o /dev/null -w '%{http_code}\n' "$(cat ~/.config/auto-pigeon-companion/api-url)"
403
$ port=$(sed -E 's#.*:([0-9]+)/?$#\1#' ~/.config/auto-pigeon-companion/api-url)
$ sudo -u someone-else curl -s -o /dev/null -w '%{http_code}\n' "http://[::ffff:127.0.0.1]:$port/"
403
```

**An installed profile is replaced only by its own publisher.** A profile's id
is its publisher's own name for it, and two accounts may publish the same one.
Installing from the catalog therefore records which listing and which account a
document came from, and an update must come from that same listing
(`NEW_353A`). A publication that claims a built-in's id, the id of a profile you
wrote or pasted here, or the id of something installed from another account is
refused before anything is written — your program paths, arguments, approvals
and pipelines stay exactly as they were. Your account's own profiles coming back
through **Sync** may replace the copy you wrote here. A profile installed before
this release has no recorded origin, so an update to it is refused rather than
guessed; remove it and install it again if you mean to replace it.

```console
$ companion toolchain install <listing-id> --approve
error: publish: that id is already installed from somewhere else: community.example.q1-tools is installed from another listing or another account; this publication cannot replace it
nothing was written: the installed document, its program paths, arguments and approval are unchanged.
```

This release writes its binding store as `aucom.local-binding/1.5`. An older
Companion refuses a 1.5 store by name; keep a copy of `bindings.json` before the
first run if you may go back.

## Configuration

Settings are saved in a per-user `config.json`, written with mode 0600 because
it holds your Auto-Pigeon session:

| System | Path |
| --- | --- |
| Linux | `~/.config/auto-pigeon-companion/config.json` |
| macOS | `~/Library/Application Support/auto-pigeon-companion/config.json` |
| Windows | `%AppData%\auto-pigeon-companion\config.json` |

Job records and downloaded map revisions live in the matching cache directory.

Two Companions — the app and a `companion` command in a terminal, say — never
overwrite each other's change to the same file: each write takes a lock first.
The lock is a small `.<file>.lock` record naming who holds it, and every change
to it is made under the operating system's lock on a `.<file>.lock.guard` beside
it, which stays there and is released by the system if a Companion is killed. A
Companion that is waiting tells you which process holds the file. This works
between Companions on this computer's own disk; when you upgrade by hand, close
the old Companion (and any `companion` command it started) before starting the
new one, because Companions up to 1.332 do not take the guard.

```console
$ ls -A ~/.config/auto-pigeon-companion | grep lock
.config.json.lock.guard
```

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
| `AUCOM_TOOLS_DIR` | where the Companion installs its build tools (also `managed_tools_dir` in `config.json`) |
| `AUCOM_MANAGED_TOOLS` | `off` keeps the Companion from setting up or checking its build tools at all |
| `AUCOM_AUE_BINARY` | a development build's extractor instead of the bundled one; shown as **unverified** everywhere |
| `AUCOM_ENV_FILE` | a `.env` to read instead of `./.env` (development only) |

## Removing it

Delete the unpacked folder. To remove what the Companion kept on this computer —
settings, session, approvals, job history, caches — and the `autopigeon://`
handler when it opens this copy (a handler that opens another Companion is left
alone, and the command says so), look first, then confirm:

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
