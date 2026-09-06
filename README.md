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
  serve [--port <n>] [--open]                                     run the local GUI server without opening a browser
  auth login [--email <address>] | status | logout                authenticate against auto-pigeon-backend
  build [--tool <name>] [--tool-version <v>] [-- <tool args>...]  run an external map-building tool
  launch <game> [--map <name>] [--game-root <dir>] [--dry-run]    launch a game using its AUB launch config
  extractor version                                               run the bundled auto-pigeon-extractor (AUE)
  migrate                                                         fold Launcher and older Companion configuration into the current one
  version                                                         print the build version
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
