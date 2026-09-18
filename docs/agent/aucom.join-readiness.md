---
id: aucom.join-readiness
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: Joining is one readiness model, and a link starts nothing
authority:
  - aucom-join-readiness
  - aucom-join-content
  - aucom-uri-handler
topics:
  - join
  - game
  - live-game
  - hosted-game
  - readiness
  - ticket
  - stage
  - assetsync
  - uri
  - autopigeon-scheme
  - engine
  - host
paths:
  - internal/joinready/**
  - internal/joincontent/**
  - internal/joinintent/**
  - internal/hostgame/**
  - internal/assetsync/**
  - internal/urischeme/**
---

# Joining is one readiness model, and a link starts nothing

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 626-675 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

## 9. JOINING IS ONE READINESS MODEL, AND A LINK STARTS NOTHING (`AUB/AUG/AUCOM/AUT 244F`)

`20260916 244F` made a hosted game joinable from the Companion's own Games area,
with verified map files. What it left behind binds a later prompt.

**`internal/joinready.Assess` is the one answer to "can this machine join".** The
CLI (`game ready`, `game join`), the local API (`/api/v1/games/...`) and the page
all render its report. Never compute a join state anywhere else, and never store
one: it is derived from AUB's current answer and this machine's current state
every time. `StateReadyForReview` is reported only after the JOB SERVICE previewed
the command — a surface that shows Join on the strength of green steps is the
failure this exists to stop.

**Four things are kept apart and must stay apart.** AUB's Game Profile (read,
never installed), the Engine Profile (local, approved per digest), the local
binding (where the owned program and game are), and join content (redistributable
client files). The host's runtime is a compatibility requirement and NEVER picks
an executable. A host's published engine profile installs through
`publish.PlanInstall`/`Apply` as `community` and nowhere else; the install
re-plans and refuses when the digest changed since the review.

**A ticket is spent only immediately before the review.** Setup (profile install,
bindings, downloads) never mints or redeems one. `hostgame.Joiner.Prepare` mints,
redeems, revalidates revision, package and endpoint against the report, and
previews; `Launch` refuses an unapproved or expired plan and returns the running
job for a second launch of the same join. The page keeps one
`hostgame.Coordination` per process — a joiner per request with its own lock would
let two tabs start two engines.

**Join content is re-checked here, verified through assetsync, and staged
atomically — and the owned game is never written.** `internal/joincontent.Check`
applies AUB's manifest rules again on this machine; `Verify` recomputes the
aggregate digest (pinned in both repositories: `sha256:cdd70f33…62cf`);
`Stager.Stage` builds a whole tree in a temporary directory and renames it into
place; `Lookup` refuses any extra file, link or changed byte. The engine is pointed
at a MANAGED base directory whose base-game folders are symbolic links to the
user's installation. Do not stage into `game_root`, and do not copy game data.
A Quake 1 `join_server` passes `-basedir .` and runs in the stage, because vkQuake
keeps only 255 characters of its command line (`TestAQuake1JoinPutsNoFolderOnTheCommandLine`);
do not put a folder path back on that line. The engine writes its settings into
the stage, so `Joiner.Assess` rebuilds a verified-but-spoiled stage from the
object store — never under an active join, and never by relaxing `Lookup`.

**The `autopigeon://` handler is `game open`, and it has no approval flag.** It
validates the link's shape before anything else, records it through
`internal/joinintent` (the ticket for at most its two-minute TTL, removed once
redeemed, deduplicated by digest), and starts or raises the page. The server
redeems it once when the page asks. Never make the handler resolve, download or
launch.
