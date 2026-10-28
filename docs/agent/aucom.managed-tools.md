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
paths:
  - internal/managedtools/**
  - internal/web/managedtools.go
  - internal/web/assets/tools.js
  - internal/cli/tools_cmd.go
  - internal/cli/managedtools_*.go
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
7. **Never switch under work.** Each version is its own folder, never changed after its rename.
   Activation waits until no job's pinned program is in the old folder and no pipeline holds
   `Server.toolGate` (held by every `build.Runner.Run`), and writes bindings under the gate's
   exclusive side. The previous version is kept; `prune` removes only what nothing uses, keeping at
   most three.
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
