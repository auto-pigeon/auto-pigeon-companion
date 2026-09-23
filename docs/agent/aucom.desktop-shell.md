---
id: aucom.desktop-shell
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: The desktop shell is deferred, and the browser is not a fallback for it
authority:
  - aucom-desktop-shell-deferral
topics:
  - desktop
  - shell
  - wails
  - webview
  - window
  - native-ui
  - electron
  - tauri
  - cgo
  - lifecycle
  - lease
  - quit
  - exit
paths:
  - cmd/companion/**
  - internal/web/open_browser.go
  - internal/web/lifecycle.go
  - internal/web/lifecycle_api.go
  - internal/web/lease.go
  - internal/web/assets/lifecycle.js
  - internal/cli/serve_lifecycle.go
---

# The desktop shell is deferred, and the browser is not a fallback for it

`AUCOM/AUE/AUT 246I1` asked for a native desktop window, said to use **Wails**
unless a short written spike proved a concrete blocker in the live code, and
said in the same breath that the shell must not block Phase A. The spike was
run. It found a blocker, and the decision is recorded in
`$MAPPER_ROOT/LLM/docs/adr/0026-the-companion-desktop-shell-is-deferred-behind-a-measured-cgo-blocker.md`,
which is the authority on the reasoning. What binds a task here is below.

## The measured finding, in one sentence

**Wails v2.16.0 COMPILES with `CGO_ENABLED=0` for every one of this project's
six targets, and the program it produces does not work.** Built that way on
Linux and run, it prints

```text
wails.Run: Wails applications will not build without the correct build tags.
```

and exits zero.

That is `internal/release`'s rule in its purest form: a build is not a
verification. Six green cross-builds would have been six statements that
something compiles and no statement at all about whether a window opens. A
release matrix that took them as evidence would have shipped a desktop binary
that starts and does nothing, on five platforms nobody had run it on.

## What that costs, and why it is a deferral rather than a refusal

A working Wails build needs CGO and the platform's WebView development
libraries — webkit2gtk on Linux, Cocoa on macOS, WebView2 on Windows. That is
not an objection to Wails; it is what using the operating system's WebView
means, and it is still the right choice for this program over Electron's
bundled Chromium or Tauri's second Rust application stack. What it costs THIS
repository is specific:

- the six-target cross-build in `build.yml` and `build/release.sh` is
  `CGO_ENABLED=0` from one Linux runner, and every one of those six targets
  would have to move to a native runner with the platform's WebView SDK;
- `build/release.sh`'s determinism claim rests on `CGO_ENABLED=0 -trimpath`,
  which is what stops the building machine reaching the binary. CGO puts the
  host's libc back in;
- there is no Apple Developer ID and no Authenticode certificate, so a
  `.app` and an installer would be unsigned in a way a loose binary is not
  obviously unsigned.

None of that is unsolvable and none of it belongs in the same task as the
one-click demo. Hence: deferred, with the spike as evidence rather than as an
opinion.

## The rules a task here inherits

- **`companion serve` and the external browser are the SHIPPING path, not a
  fallback.** The prompt's "make desktop mode the only recovery path" is
  forbidden in the other direction too: until a desktop shell exists and has
  been run on each platform, the loopback server with its per-run API token,
  Host and Origin checks is how this program is used, and it is not to be
  described as a degraded mode.
- **Adding Wails means moving the release matrix to native runners in the same
  task.** A desktop entrypoint added to the existing pure-Go build is a binary
  that compiles for six platforms and opens a window on one.
- **The entrypoint is a separate package behind a build tag, and
  `./cmd/companion` stays pure Go.** A dependency in `go.mod` is harmless; a
  dependency the CLI binary links is the six-target cross-build gone.
- **Never track a moving branch.** Wails v3 is prerelease; v2 is stable at
  v2.16.0. Pin an exact tested version or use v2, and record which.
- **One backend, two hosts — never two implementations.** Whatever host a
  future task adds, it starts the SAME application services and serves the SAME
  embedded assets. There is no second implementation of auth, profiles, builds,
  jobs, extractor acquisition, staging or launch, and a Wails bridge exposes
  only narrow file/folder/window operations with the domain actions left on the
  existing API.
- **The token, Host and Origin boundary does not weaken inside a WebView.** A
  page is not trusted because of where it is displayed.

## The browser owns the foreground lifecycle (NEW_247B)

Closing the last page closes the program; the decision and its reasoning are
`$MAPPER_ROOT/LLM/docs/adr/0030-the-companion-stops-when-its-last-page-lease-ends.md`.
What binds a task here:

- **Two modes, and `serve` stays server mode.** The no-subcommand launch is
  interactive (`serve --open --interactive`); `companion serve` and
  `--stay-running` never stop because no page is attached. Scripts, harnesses
  and `auto-pigeon-tools` start `serve` and must not start exiting on their own.
- **The lease is the only authority.** One WebSocket per page at
  `GET /api/lifecycle/lease` (`internal/web/lease.go`), counted by
  `internal/web/lifecycle.go`. Never `beforeunload`/`sendBeacon`, and never
  the handle of the process that opened the browser — `xdg-open` and `start`
  return before any window exists.
- **No second local authentication.** The lease carries the existing per-run
  token as the subprotocol `aucom.token.<token>`, read only on an upgrade
  request, behind the same guard. No bootstrap URL value, no cookie.
- **No server-side silence deadline on a lease.** A hidden tab is throttled or
  frozen and is still open; a closed one closes its loopback socket at once.
- **Work outlives the page.** Anything in `Server.ActiveWork` keeps an
  interactive process alive; a new long-running kind of work must be added
  there, or closing a tab will stop it.
- **Shutdown order is `Server.Close` then the job service's `Close`:** hosted
  listings end first (`host_stopped`), Build & Run and builds are cancelled
  and waited for, then processes stop gracefully-then-forcefully.
- **The terminal is two lines and one causal exit line** in interactive mode;
  detail goes to `companion.log`. Server mode's output is unchanged.

