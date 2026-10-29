---
id: aucom.managed-tools
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: Two official toolchains are installed and updated by one owner, and nothing else is downloaded
authority:
  - aucom-managed-toolchains
  - aucom-managed-tool-updates
topics:
  - managed-tools
  - managedtools
  - ericw-tools
  - q3map2
  - install
  - update
  - github-release
  - first-launch
  - setup-card
  - skipped-version
  - self-update
  - selfupdate
paths:
  - internal/managedtools/**
  - internal/web/managedtools.go
  - internal/web/assets/tools.js
  - internal/cli/tools_cmd.go
  - internal/cli/managedtools_*.go
  - internal/selfupdate/**
  - internal/web/selfupdate.go
  - internal/cli/selfupdate_cmd.go
  - docs/adr/0010-the-companion-installs-and-updates-two-official-toolchains.md
---

# Two official toolchains are installed and updated by one owner, and nothing else is downloaded

HITL, 2026-10-28 (`NEW_367`): the Companion installs the Auto-Pigeon builds of ericw-tools and
q3map2 on first launch and, while it runs, checks their GitHub releases about hourly and OFFERS
updates. [ADR-0010](../adr/0010-the-companion-installs-and-updates-two-official-toolchains.md) is the
decision record; this module is the rules a change to the code must keep. The Companion's own
update belongs to `NEW_368`, which consumes this coordinator and its verification.

## The rules

1. **One owner, a closed allowlist.** `internal/managedtools` is the only package that downloads a
   program. `Tools` (two repositories, two tag families, the manifest schemas this build reads)
   and `githubHosts` are compiled in. Nothing a profile, binding, config file, environment variable
   or page says can add a source, a host or a URL. `NewLoopbackSource` refuses every non-loopback
   host; the only non-test caller is behind the `aucom_fixture_releases` build tag, which no
   release build uses.
2. **Generic acquisition stays closed.** `internal/acquire`, `internal/aue` and `internal/profile`
   import no HTTP client (`TestNothingThatFindsAProgramCanDownloadOne`). `managed_download` in a
   document is read and refused. `managed_official` is a BINDING mode and is not in the document
   schema's list of modes — do not add it there.
3. **Eligibility before bytes.** Drafts never; prereleases only on the beta channel (the shipped
   default, `DefaultChannel`); versions ordered by `Tool.Version`, never by text or date; nothing
   older than `Tool.Baseline` (kept equal to the built-in fork profile's pin); a known manifest
   schema whose tag, version and prerelease flag agree with GitHub; a published target for this
   OS/processor with a passing acceptance and a SHA-256 and size agreeing with GitHub's listing
   and digest; a banner the strict built-in profile accepts and does not refuse. An unknown schema
   is "unsupported", never guessed at.
4. **Verify, then install, then ask the programs.** Archive digest against the manifest and
   GitHub; bounded extraction into staging refusing escaping/absolute/linked/device/case-colliding
   entries; every file against the archive's `MANIFEST.json`; program headers against this
   processor; one rename. Then `job.Service.ProbeAt` — the executor's own version check — before
   anything is bound. A failure removes the installation; the active one is untouched.
5. **Bind only what nobody chose.** `web.bindManaged` rebinds a profile only when it is unbound,
   has no programs, or is already `managed_official`; all of a tool's profiles in one
   `binding.Update`. A person's own program clears `managed` and is never replaced; Settings and
   `companion tools use-managed` are the only way back.
6. **Offer, never apply unasked.** Update now / Update when I close Companion / Cancel, and "Don't
   ask again for this version" suppresses exactly `(tool, version, asset id, sha256)`. A Cancel
   without it is not shown again in this process, nor for a day. A version seen with other bytes is
   an `IntegrityEvent`, never an install. Suppression is per tool and never shared with the
   Companion's own update.
7. **Never switch under work — in ANY process sharing the tools folder (01_NEW_368).** Each
   version is its own folder, never changed after its rename. `internal/tooluse` is the one owner
   of the cross-process protocol, in `<tools>/.in-use/` (the folder's canonical path, so two
   configurations sharing it share it, and two folders never block each other):
   - every `build.Runner.Run` — server and `companion build run` alike — takes a **use lease**
     (`build.Options.Lease`, keys `companion` + each managed toolchain of its stages) *before* it
     asks any program anything, freezes every tool profile's `Executables` with their digests,
     runs every stage with those paths (`job.Request.Executables`) and refuses a stage whose frozen
     program changed; the lease is held until `Run` returns;
   - activation and pruning take the toolchain's **switch gate** and then read the leases; an
     "Update now" KEEPS the gate while it waits (`Manager.gates`), so new leases wait at most
     30 s (server) / 2 min (CLI) and are refused retryably (`503 tools_updating`, Auto-build
     `autobuild.ErrNotNow`: not consumed) — the switch is not starved; the gate is dropped when
     the intent ends;
   - **a gate's pathname is not its owner (NEW_368 gate ownership).** Publishing a gate, judging
     it abandoned and removing it, refreshing its heartbeat and releasing it all happen holding
     the folder's **guard** — the OS lock (flock / `LockFileEx`) on `.in-use/guard.lock`, a file
     never removed, which the kernel lets go of when its holder dies — so what is removed is what
     was judged. It is held for a few file operations, never through a build, download or wait
     (bounded at 10 s; a lease needs it only to reclaim an abandoned gate). Each gate carries an
     immutable `id`; its holder refreshes or releases it only while the record at the path carries
     that id, and `Manager.switchGate` re-checks `Gate.Held` before trusting a gate it kept, so a
     holder whose gate was reclaimed (no heartbeat for a stale period, as across a sleep) neither
     touches the replacement nor activates under it. A gate that cannot be read is held until it
     has gone a stale period unrefreshed. Companions from before the guard (≤ v1.325) do not take
     it: the race stays possible only between one of them and a new one;
   - liveness is pid + start time against the kernel (`job.ProcessRunning`), a heartbeat where
     that is unknown. Switching does not wait for a dead lease. Removal does, while a job its
     writer launched (read from the job store the lease names) still runs as the same process, or
     cannot be verified, for up to 48 h. Nothing here ever signals a process.
   The in-process `Server.toolGate`/`Host.Busy` stay as the in-process half. The previous version is
   kept; `prune` removes only what nothing references, keeping at most three.
8. **Schedule.** About hourly with jitter, claimed in the state file so a second Companion does not
   check too; a restart does not reset it; failures back off 1, 2, 4 … 60 minutes; `Retry-After` and
   an exhausted rate limit are honoured and shown; a 304 is a successful check (but a list whose
   evaluation hit a manifest FETCH error is not cached behind its ETag). No goroutine outlives
   `Run`'s context, and nothing is scheduled outside the process.
9. **Close.** "Update when I close" is applied after the server and the executor have closed, on a
   normal exit only; an update that a crash left chosen-and-staged is applied by `ApplyAtStart`
   before anything runs. Bindings activated at a close keep their facts cleared, and the next start
   asks the programs.
10. **Who runs it.** The application (`companion`, `companion --stay-running`) runs the coordinator;
    `companion serve` only with `--managed-tools on`; `managed_tools_off`, `AUCOM_OFFLINE` and
    `AUCOM_MANAGED_TOOLS=off` stop checks and installs and keep the status. ONE coordinator per
    configuration: a `serve` that finds another Companion answering for its config (asked before it
    replaces the token file) runs none, and the coordinator — with its at-start apply — starts only
    after the process has bound its port (found live: a second instance applied an "on close" update
    at its start while the first was still running). Every test that starts
    a server keeps it off (`internal/cli` sets `AUCOM_MANAGED_TOOLS=off` and its own
    `XDG_DATA_HOME`): no test may reach GitHub or this machine's data folder.

11. **The Companion's own update (NEW_368) is a third target on the same coordinator**, never
    a second downloader. `managedtools.Self` is added by `Options.Self`; there is no first
    install of oneself; the bundle is checked against both manifests and proved before it is
    staged. Only `internal/selfupdate` writes the Companion's own folder: the folder comes from
    `InspectSelf` (the executable), never from the journal or a request; the files are the old
    and new bundle manifests' members only; every step is journaled in
    `<bundle>/.companion-update/`, proved in place, rolled back on failure, at the next start
    after an interruption, or after three unready starts. `portable` is replaced; `manual` (a
    folder it cannot write, macOS) is downloaded and left for a person; `development` is never
    replaced. Update now waits for `ActiveWork`, the tool gate and any toolchain install, then
    closes the admission fence (`503 companion_updating`) and stops with `ExitUpdate`; the
    after-serve action replaces and relaunches with fixed arguments (mode, `--debug`, the same
    port) once every defer has run, and puts the old one back if the new one does not serve
    within 90 s. A live fixture candidate that refuses to serve is
    `AUCOM_FIXTURE_FAIL_SERVE=<version>` in an `aucom_fixture_releases` build only.

12. **The self-update transaction is write-ahead and reconciled by bytes (01_NEW_368).** The
    journal (`aucom.self-update-journal/2`) is written once, before anything moves, with every
    owned path's old and new SHA-256; nothing during the swap is recorded as done. Forward and
    reverse passes are idempotent and decide from the files at the path, in `previous/` and
    `incoming/`; a file that is neither bundle's is never moved (`recovery_needed`).
    `rolled_back` is written only after every member is read back; otherwise `recovery_needed`
    (non-terminal): a start then refuses to serve, no new update starts, and nothing kept is
    removed. `selfupdate recover` finds its bundle from where the program is, so the copy in
    `previous/` can run it when a Windows rename-aside left no program at the path. A 1.323
    (`/1`) journal is read, confirmed in its own schema (1.323's updater waits for it), and an
    interrupted one is undone by file location. The update lock breaks a dead holder at once
    (`lockfile.Options.BreakDeadHolder`), judged and broken under the lock's OS guard, so two
    Companions starting an update at that moment never both activate
    (`TestTwoUpdatesThatFindADeadHoldersLockNeverBothActivate`, NEW_368 post-push review). A
    Companion from before the guard (≤ v1.332) still running beside a new one is not excluded by
    it: the self-update starts the new one after the old one's locked steps, and a manual upgrade
    closes the old Companion first. Abrupt process death is PROVEN (children killed at every
    boundary, `internal/selfupdate/crash_test.go`); power loss is argued from fsync ordering, not
    tested. An "Update now" or a close also takes the `companion` switch gate and waits for other
    processes' leases.

## Trust, said honestly

The digests are published in the same release as the archive: they show integrity, not who
published it. Nothing may display "signed" or "verified publisher". The accepted residual is threat
row T55; T05 and T56 are the other rows this owner answers for.

## Where it lives on disk

`config.Config.Tools()`: `AUCOM_TOOLS_DIR`, else `managed_tools_dir`, else the per-user DATA
directory (`$XDG_DATA_HOME` or `~/.local/share`, `~/Library/Application Support`,
`%LocalAppData%`) + `auto-pigeon-companion/tools`. `managed-tools.json` (schedule, offers, choices)
sits beside `config.json`; each installation's `install.json` is its provenance record and a
build's manifest copies the binding's `managed` into `tools[].managed`.
