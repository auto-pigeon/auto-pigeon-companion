# Auto-Pigeon Companion

Closed-source desktop application for local Quake map geometry work. It signs
in against **AUB** ([auto-pigeon-backend][aub], a Pocketbase instance) and
performs map operations locally by driving **AUE**
([auto-pigeon-extractor][aue]) as a subprocess.

This repository is currently a **structural bootstrap**: the architecture,
build tooling, and package boundaries are in place; the map operations
themselves are not implemented yet.

## How it works

There is no GUI toolkit and no embedded browser engine here. The binary:

- serves a local `net/http` server bound to `127.0.0.1`;
- serves a frontend of plain HTML/CSS/JS compiled into the executable with
  `//go:embed` — no npm, no bundler, no build step;
- opens that URL in whatever browser the user already has installed.

The result is a single CGO-free executable that cross-compiles to all six
supported targets with nothing but `GOOS`/`GOARCH` and `go build`.

Dependencies: none. `go.mod` lists no third-party modules, and there is no
`go.sum`, because everything used is in the standard library. That includes the
CLI dispatcher (hand-rolled, mirroring AUE's `internal/cli`) and the AUB REST
client.

AUE is **not** imported as a Go library — its packages live under `internal/`,
which Go's visibility rules put out of reach of another module. AUC calls AUE's
CLI subcommands as a subprocess and reads their JSON from stdout, behind the
`aue.Runner` interface.

## Usage

Launching with **no subcommand** is GUI mode — it starts the server and opens
the browser. Named subcommands run headless.

```console
$ companion
companion is serving http://127.0.0.1:41235/

$ companion version
0.1.0-dev

$ companion --help
companion — Auto-Pigeon Companion: local map operations with an AUB account
...
```

Serve on a fixed port without launching a browser (useful over SSH, or when
scripting against the JSON API):

```console
$ companion serve --addr 127.0.0.1:8777 --no-browser
companion is serving http://127.0.0.1:8777/
```

The listen address must be a loopback address; the server refuses anything
else, because it exposes an authenticated session.

### Authentication

```console
$ printf '%s' "$AUB_PASSWORD" | companion auth login --email andrea@example.com
signed in as andrea@example.com

$ companion auth status
signed in as andrea@example.com (http://127.0.0.1:8090)

$ companion auth logout
signed out locally
```

`--password` is accepted but reads better from stdin, which keeps the secret
out of shell history and the process table. The session token is stored in the
per-user config file (mode 0600):

| OS | Path |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/auto-pigeon-companion/config.json` (default `~/.config/…`) |
| macOS | `~/Library/Application Support/auto-pigeon-companion/config.json` |
| Windows | `%AppData%\auto-pigeon-companion\config.json` |

`auth logout` forgets the local token. Pocketbase record tokens are stateless,
so it does not invalidate the token server-side.

### Local JSON API

The frontend talks to these routes; they are same-origin-guarded, so `curl`
works (no `Origin` header) but another web page cannot drive them.

```console
$ curl -s http://127.0.0.1:8777/api/status
{"version":"0.1.0-dev","aub_base_url":"http://127.0.0.1:8090","authenticated":false,"aue_available":false}

$ curl -s http://127.0.0.1:8777/api/aue/version
{"version":"0.2.0"}

$ curl -s -X POST http://127.0.0.1:8777/api/auth/login \
    -H 'Content-Type: application/json' \
    -d '{"identity":"andrea@example.com","password":"…"}'

$ curl -s -X POST http://127.0.0.1:8777/api/auth/logout
{"authenticated":false}
```

## Building

```console
$ go build ./cmd/companion
$ go test ./...
```

A plain `go build` produces a companion with **no embedded extractor**, which
is the normal development state. Point it at a locally built AUE instead:

```console
$ AUC_AUE_BINARY=../auto-pigeon-extractor/bin/auto-pigeon-extractor companion
```

### Embedding the extractor

The release build embeds the platform-matching AUE binary into the companion
executable, so there is no runtime download and no version skew. Because
`//go:embed` resolves at compile time, the ordering is strict:

```console
$ cp "$AUE_BINARY_FOR_TARGET" internal/aue/embedded/auto-pigeon-extractor
$ GOOS=windows GOARCH=amd64 go build -o dist/windows-amd64/companion.exe ./cmd/companion
$ rm -f internal/aue/embedded/auto-pigeon-extractor
```

Exactly one non-`.gitkeep` file may sit in that directory; the runner refuses
to guess if it finds more. The binary is never committed — see `.gitignore`.

> **Open question.** Where CI sources that binary (build AUE from source as a
> prior step, or download a tagged release artifact) is undecided. See
> `TODO(confirm-aue-build-source)` in `.github/workflows/build.yml`.

### Cross-compiling

All six targets are CGO-free, so there is no Docker, no cross-toolchain, and no
`fyne-cross`:

```console
$ for target in windows/amd64 windows/arm64 linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
    GOOS="${target%/*}" GOARCH="${target#*/}" CGO_ENABLED=0 \
      go build -trimpath -ldflags "-s -w -X main.version=0.1.0" \
      -o "dist/${target%/*}-${target#*/}/companion" ./cmd/companion
  done
```

`.github/workflows/build.yml` runs exactly this matrix on every push as a
verification step.

## Packaging

**Windows** — [Inno Setup][inno], `build/windows/installer.iss`, compiled on a
`windows-latest` runner:

```console
> iscc /DAppVersion=0.1.0 /DBinaryPath=dist\windows-amd64\companion.exe /DOutputDir=dist\installers build\windows\installer.iss
```

**Linux** — [nfpm][nfpm] for `.deb`/`.rpm`, plus a `.tar.gz` fallback:

```console
$ VERSION=0.1.0 ARCH=amd64 BINARY=dist/linux-amd64/companion \
    nfpm package -f build/linux/nfpm.yaml -p deb -t dist/
$ tar -C dist/linux-amd64 -czf dist/auto-pigeon-companion-0.1.0-linux-amd64.tar.gz companion
```

**macOS** — a `.app` bundle is just a directory with a known shape, so
`build/macos/make-app-bundle.sh` assembles one directly (it runs on Linux CI
too):

```console
$ ./build/macos/make-app-bundle.sh --binary dist/darwin-arm64/companion --version 0.1.0 --out dist/darwin-arm64
built dist/darwin-arm64/Auto-Pigeon Companion.app
packaged dist/darwin-arm64/auto-pigeon-companion-0.1.0.app.zip
```

### Signing — not done

Nothing here is code-signed or notarized; no certificates exist yet. Until they
do:

- **macOS** blocks the unsigned bundle on first open. The user must
  right-click → **Open** and confirm, or run
  `xattr -dr com.apple.quarantine "Auto-Pigeon Companion.app"`.
- **Windows** SmartScreen warns on the unsigned installer ("More info" → "Run
  anyway").

## Status and open questions

Structural only. Both integrations are stubbed behind interfaces:

- **AUB auth** is written against the generic Pocketbase auth-with-password
  shape. The collection name, the base URLs for dev/staging/prod, and the
  record fields are all unconfirmed — see `TODO(confirm-aub-auth-shape)` and
  `TODO(confirm-aub-base-url)`.
- **AUE embedding** assumes embed-at-build-time rather than
  download-at-runtime; confirm before it hardens.
- **`LICENSE`** is placeholder proprietary wording pending final text.

[aub]: https://github.com/andrea-dintino/auto-pigeon-backend
[aue]: https://github.com/andrea-dintino/auto-pigeon-extractor
[inno]: https://jrsoftware.org/isinfo.php
[nfpm]: https://nfpm.goreleaser.com/
