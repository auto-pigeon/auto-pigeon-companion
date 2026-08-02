# auto-pigeon-launcher

Desktop application that authenticates against
[auto-pigeon-backend](https://github.com/andrea-dintino/auto-pigeon-backend)
(AUB), builds Quake maps using external map-building tools, and launches games
using per-game launch configuration stored in AUB.

MIT licensed — see [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

> **Status: structural bootstrap.** The scaffolding, the CLI, the local GUI, the
> tool pipeline and the launch pipeline all run today, but against stubs: no real
> map-building tool is wired up, and launch configs are hardcoded rather than
> read from AUB. See [Open questions](#open-questions).

## What it is

One binary, two modes:

- **GUI mode** — run it with no arguments. It starts an HTTP server on
  `127.0.0.1` and opens the page in your default browser.
- **CLI mode** — named subcommands (`auth`, `build`, `launch`, `serve`,
  `version`) run headless for scripting.

There is **no GUI toolkit and no embedded browser engine**. The frontend is
plain HTML/CSS/vanilla JS compiled into the binary with `//go:embed`, and your
own browser is the window. That is what keeps the build CGO-free and
cross-compilable to all six targets with plain `go build`.

## Build

Requires Go 1.23 or newer. No CGO, no Docker, no npm, no build step for the
frontend.

```bash
go build -o auto-pigeon-launcher ./cmd/launcher
go test ./...
```

Cross-compile any of the six supported targets:

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/windows-amd64/auto-pigeon-launcher.exe ./cmd/launcher
GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build -o dist/windows-arm64/auto-pigeon-launcher.exe ./cmd/launcher
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -o dist/linux-amd64/auto-pigeon-launcher   ./cmd/launcher
GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -o dist/linux-arm64/auto-pigeon-launcher   ./cmd/launcher
GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -o dist/darwin-amd64/auto-pigeon-launcher  ./cmd/launcher
GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -o dist/darwin-arm64/auto-pigeon-launcher  ./cmd/launcher
```

Set the version at build time:

```bash
go build -ldflags "-X main.version=0.1.0" -o auto-pigeon-launcher ./cmd/launcher
```

## Usage

### GUI

```bash
./auto-pigeon-launcher
```

Prints the URL it bound and opens it in the default browser:

```
auto-pigeon-launcher 0.1.0-dev listening on http://127.0.0.1:8789/
```

If no browser opens (a headless machine, no `xdg-open`), the URL is still
printed — open it yourself. To run the server without opening anything:

```bash
./auto-pigeon-launcher serve --port 9000
```

The server binds loopback only and is never reachable from the network.

### Authenticate against AUB

```bash
# Password from an environment variable, for scripts:
AUL_PASSWORD='…' ./auto-pigeon-launcher auth login --email you@example.com

# Or piped on stdin:
printf %s 'your-password' | ./auto-pigeon-launcher auth login --email you@example.com

./auto-pigeon-launcher auth status
./auto-pigeon-launcher auth logout
```

`auth status` prints the configured AUB instance and whether a session is
stored:

```
aub: http://127.0.0.1:8090
signed in: yes (you@example.com)
expires: 2026-08-16 14:22 (local estimate)
```

Typing a password at the prompt echoes it visibly — disabling terminal echo
would need a dependency this repository does not have. Prefer `AUL_PASSWORD` or
a pipe.

`auth logout` forgets the token locally. It is not revocation: AUB issues
stateless tokens that stay valid until they expire.

### Build a map

Runs the full external-tool pipeline — resolve, download, checksum-verify, run,
stream output. Today that pipeline runs a **fake tool**, because no real
map-building tool is chosen yet:

```bash
./auto-pigeon-launcher build -- --example-tool-argument
```

```
resolved noop@0.0.0-fake (linux/amd64)
verified /home/you/.cache/auto-pigeon-launcher/tools/noop/0.0.0-fake/linux-amd64/noop
[noop] fake tool 0.0.0-fake
[noop] executable: /home/you/.cache/auto-pigeon-launcher/tools/noop/0.0.0-fake/linux-amd64/noop
[noop] args: --example-tool-argument
[noop] no real map-building tool is wired up yet
[noop] done
```

Everything after `--` is passed to the tool verbatim. `--tool <name>` selects a
different tool and currently fails with "unknown tool", which is correct: the
registry is empty until the tools are chosen.

### Launch a game

```bash
# Show the resolved command without starting anything:
./auto-pigeon-launcher launch quake --map e1m1 --game-root /games/quake --dry-run
```

```
/games/quake/quakespasm -basedir /games/quake +map e1m1
```

```bash
# Actually start it:
./auto-pigeon-launcher launch quake --map e1m1 --game-root /games/quake
```

Launch configs are currently hardcoded examples, not read from AUB. `--game-root`
can be omitted once a per-game directory is stored in the config file under
`game_roots`.

## HTTP API

The GUI's own API, served on loopback. Useful for scripting against a running
instance:

```bash
curl -s http://127.0.0.1:8789/api/status
curl -s -X POST http://127.0.0.1:8789/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"…"}'
curl -s http://127.0.0.1:8789/api/launch-configs
curl -s -X POST http://127.0.0.1:8789/api/build \
  -H 'Content-Type: application/json' -d '{"tool":"noop","args":[]}'
curl -s -X POST http://127.0.0.1:8789/api/launch \
  -H 'Content-Type: application/json' \
  -d '{"game":"quake","map":"e1m1","game_root":"/games/quake","dry_run":true}'
```

There is no per-request authentication yet — see
[`internal/web/server.go`](internal/web/server.go).

## Configuration

`config.json` in the OS config directory, written with mode `0600` because it
holds the AUB session token:

| Platform | Path |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/auto-pigeon-launcher/config.json` (or `~/.config/…`) |
| macOS | `~/Library/Application Support/auto-pigeon-launcher/config.json` |
| Windows | `%AppData%\auto-pigeon-launcher\config.json` |

Downloaded external tools are cached under the OS **cache** directory
(`~/.cache/auto-pigeon-launcher/tools` and its equivalents) — re-fetchable
content, so clearing it costs only download time.

```json
{
  "aub_base_url": "http://127.0.0.1:8090",
  "port": 8789,
  "game_roots": { "quake": "/games/quake" }
}
```

## Layout

```
cmd/launcher/      thin entrypoint: streams, exit code, version string
internal/cli/      hand-rolled subcommand dispatcher (no cobra)
internal/web/      loopback HTTP server, JSON API, embedded frontend
internal/tools/    external GPL-2.0 tool pipeline; fake tool stand-in
internal/launch/   launch config + cross-platform process launch
internal/aub/      auto-pigeon-backend REST client
internal/config/   local config, OS directories, tool cache path
build/             Inno Setup, nfpm, macOS app bundle
```

## Packaging

Skeletons that work, not a wired-up release pipeline. CI builds all six targets
on push; it does not publish.

**Windows** — [Inno Setup](build/windows/installer.iss), compiled with `iscc` on
a `windows-latest` runner:

```bat
iscc /DAppVersion=0.1.0 /DSourceBinary=dist\windows-amd64\auto-pigeon-launcher.exe build\windows\installer.iss
```

**Linux** — [nfpm](build/linux/nfpm.yaml) for `.deb` and `.rpm`, plus a plain
tarball:

```bash
VERSION=0.1.0 ARCH=amd64 nfpm package --config build/linux/nfpm.yaml --packager deb --target dist/
VERSION=0.1.0 ARCH=amd64 nfpm package --config build/linux/nfpm.yaml --packager rpm --target dist/
tar -czf dist/auto-pigeon-launcher-0.1.0-linux-amd64.tar.gz -C dist/linux-amd64 auto-pigeon-launcher
```

**macOS** — [make-app-bundle.sh](build/macos/make-app-bundle.sh) assembles a
`.app` around the cross-compiled binary and zips it. It runs anywhere, not only
on a Mac:

```bash
build/macos/make-app-bundle.sh dist/darwin-arm64/auto-pigeon-launcher arm64 0.1.0 dist
```

**Signing and notarization are not implemented.** No Windows code-signing
certificate and no Apple Developer ID exist, so releases are unsigned: Windows
SmartScreen and macOS Gatekeeper will warn on first launch. The exact commands to
add are documented in [make-app-bundle.sh](build/macos/make-app-bundle.sh) and
[installer.iss](build/windows/installer.iss); they are steps to perform once
credentials exist, not code to write now.

## External tools and licensing

The map-building tools are **GPL-2.0**. This repository is MIT. They coexist
because the tools are downloaded as independent binaries and run as **separate
processes** — never compiled, linked, vendored, or embedded into this binary.
Read [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) before adding a dependency
or changing how tools are obtained.

## Open questions

Blocking real implementations:

1. **Which GPL-2.0 tool(s), and which versions?** Needed before
   `internal/tools`'s registry can be populated.
2. **AUB's base URL(s), and the collection schema for per-game launch configs.**
   `internal/launch`'s AUB provider fails explicitly rather than decode a guessed
   schema.
3. **Bundle the tools in release archives, or download on first run?** Affects
   `internal/tools/manager.go` and every packaging script, and carries a GPL-2.0
   redistribution obligation if bundling wins.
