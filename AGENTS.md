# AGENTS.md — Auto-Pigeon Companion

**This file holds the rules that bind nearly every task here, the source-of-truth order, and a
routing table. Nothing else.** Every detailed rule this repository has lives in exactly one module
under `docs/agent/`, which is READ WHEN A TASK NEEDS IT and is not loaded at startup.

That is a change of shape, not of content. Before `NEW_246G` this file was 782 lines and 7,372
words and an agent paid all of it before anybody typed anything — the extractor acquisition chain,
the export gate, two work-in-progress game families, the threat matrix, the approval writer, the
retention bounds, the five native-support states and the join readiness model, in every session,
whatever the task was. `docs/agent/section-manifest.json` accounts for every line of that file and
for every one of its 28 headings, and
`../auto-pigeon-tools/scripts/agent_context_router.py gate --repo-root "$PWD"` re-derives the
accounting on every run: a rule that went missing, or a destination that has since been deleted,
fails the gate. **No rule was dropped for looking historical.**

## Do not read or run `run-sequence.sh`

`run-sequence.sh` is the operator's unattended queue drainer. At ~250 KB it is the largest file in
the workspace, it is not your task, and reading it costs the context your task needs.

**Do not read, inspect, verify or debug it, and do not invoke it.** Starting it is the operator's
own action, from their own terminal.

Other files still mention it — as one of the ways a session gets started, or in a design note.
Those mentions are background, not an instruction to go and open it. If `run-sequence.sh` looks
like the cause of whatever you are investigating, say so and stop.

## 0. What this repository is

This repository is **Auto-Pigeon Companion**, abbreviated **AUCOM**. Its expected location is
`mapper-code/auto-pigeon-companion/`. It is the program that runs on a user's own machine: it
acquires and runs the toolchains a map needs, holds the profiles and approvals that say what may
run, publishes and installs portable profiles, and joins a hosted game with verified content.

It is one of the repositories under `mapper-code/` and follows the same mapper-wide prompt/handoff
workflow as every sibling. `README.md` is the user-facing manual for what the program does — it is
large, it is maintained separately, and it is **not** an instruction payload; nothing here may be
moved into it and no session is asked to read it whole.

This file was created to carry the shared workflow rules, which this repo previously had nowhere to
record. It deliberately does **not** invent engineering rules for this codebase; add those in a
task that is actually about them, in the module that owns the subject.

`auto-pigeon-launcher` (**AUL**) was merged into this repository and retired —
`docs/agent/aucom.launcher-merge.md` is the authority, and the rule it leaves behind is that there
is one implementation of each thing.

## 1. Source of truth, in order

When two documents disagree, the earlier one in this list wins.

1. **The active prompt.** It names the work, the execution repository and the complete list of
   repositories you may change.
2. **HITL.** A decision the operator froze, recorded in a handoff or in a module here with its date.
3. **`$MAPPER_ROOT/LLM/WORKFLOW.md`** — the mapper-wide prompt/handoff protocol, shared by every
   repository. This file follows it exactly and does not restate it.
4. **`$MAPPER_ROOT/LLM/DOCTRINE.md`** — the observability doctrine. Binding here in full:
   instrument before theorising, a message without a duration is an opinion, and a handoff is the
   case law.
5. **The workspace root** — `mapper-code/AGENTS.md` and `mapper-code/CLAUDE.md`, which every
   session here already loads. It is the authority on the cross-repository boundary, where durable
   task material lives, the AUB port contract (9190) and the rule that **component addresses live
   in `.env`, never in code**. Those are not restated here;
   `docs/agent/aucom.component-addresses.md` explains why the copy that used to be was removed.
6. **This file**, for the always-rules and the routes.
7. **The module under `docs/agent/`** that the routing table names for your task. Each one is the
   single authoritative home for its rules, and the gate fails a change that gives one rule two
   homes.
8. **The code and its tests.** When a module and the code disagree, that is a finding to report,
   not a licence to pick one. In this repository that is unusually literal: `internal/threat`,
   `internal/maturity`, `internal/release/native-support.json` and `job.RetentionLimits` are
   documents the build parses, so a claim in prose that the code contradicts fails a test rather
   than merely being wrong.

## 2. The rules that bind every task in this repository

These are unconditional. Everything else is routed.

- **Resolve the task, then checkpoint, then work.** Prompts live in
  `$MAPPER_ROOT/LLM/prompts/auto-pigeon-companion/`, handoffs in
  `$MAPPER_ROOT/LLM/handoffs/auto-pigeon-companion/`. Read the newest handoff, then the prompt it
  points at, then the next prompt. Before resolving one, check for unrecorded manual work and write
  a `MANUAL_*` handoff first if any exists. Then
  `python3 ../scripts/agent_task.py checkpoint --repo-root "$PWD" --status in_progress` before any
  task edit, and a truthful `complete` / `partial` / `blocked` / `failed` plus `validate` before
  stopping. `agent_task.py checkpoint` writes are not fully trusted once more than one prompt might
  be outstanding — verify the resulting handoff's `prompt_path` before finishing.
- **New prompts** are named `YYYYMMDD_NN_Title-Case-With-Dashes.md` per the shared convention and
  carry both a `## Prerequisite` prose section and a `requires:` frontmatter block (`requires: []`
  if none). The one prompt in the archive that predates that convention has no `NN`; per
  `WORKFLOW.md`, its marker uses the whole filename stem.
- **The handoff is the deliverable, not the terminal.** Nothing decisive may exist only in command
  output, background-task output or scrollback. Create the canonical handoff near the START with
  status `in_progress`, refresh it after each coherent phase and before any long-running command or
  likely interruption, and make every checkpoint sufficient for a fresh session to continue without
  replaying the transcript.
- **Print `WORKFLOW.md`'s end-of-task marker as the literal last line of the final response**, at
  every status and however the session was started. This repo **is** routable through
  `run-agent.sh` — discovery reads `AUCOM` out of `.agent-repo.json`, and `./run-agent.sh --repos`
  lists it — but sessions here are still often started by hand, and a hand-started session gets no
  task context: the only `SessionStart` hook this repository has is graft's, which injects a
  repository map and nothing about the task. So the marker rule holds whichever way the session
  began, and so does resolving the task yourself when nothing resolved it for you.
- **`go build ./...`, `go vet ./...` and `go test ./...` must all pass before a task is complete**,
  and the suite is not decoration: it parses the threat matrix, pins the two maturity sentences,
  derives the retention bounds and checks the native-support states, so a documentation change that
  contradicts the code fails here rather than in review.
- **Never edit a sibling repository** unless the active prompt names it as a mutation target. That
  includes `auto-pigeon-launcher/`, which still exists and now carries only a deprecation notice.
  Agents may inspect siblings and run their public CLI/API when a prompt needs it.
- **Run-specific and generated material belongs under `$MAPPER_ROOT/LLM/`**, never in this
  checkout: handoffs, reports, screenshots, test logs, generated bundles and acceptance records.
- **No component may compile in where another component lives.** The workspace root is the
  authority and carries the incident this came from; `docs/agent/aucom.component-addresses.md` says
  why this file keeps no second copy, and what it means for a program that runs on somebody else's
  machine.
- **Update `README.md` in the same task** when a task adds or materially changes a user-visible
  feature, with at least one executable example per new or changed CLI command or HTTP workflow.
  *"Everything the page can do, the CLI can do too"* is a claim this repository has already had to
  repair once (`aucom.approvals`); do not let a new surface make it false again.
- **Quake II and Quake III are WORK IN PROGRESS, and no document may say otherwise.** The statement
  is `internal/maturity`, keyed on AUB's `engine_family`, pinned by SHA-256 on both sides. Never
  soften it, never derive one family's sentence from another's, and never let a profile document
  switch it off. `docs/agent/aucom.quake2-maturity.md`, `docs/agent/aucom.quake3-maturity.md`.
- **Context lifecycle is not yours.** `run-sequence.sh` owns it through a `PreCompact` hook that
  blocks compaction, checkpoints the work and starts a fresh session on the same prompt. Do not
  estimate your window, adopt a threshold, compact by hand, or stop an unattended run to ask for a
  session reset.
- **Get context from `graft` before grepping or opening source files.** This repository is indexed
  in `graft/`: `graft ask "<task>" --source` to locate and understand, `graft grep "<literal>"`
  when you need EVERY occurrence, `graft callers <symbol>` before renaming anything. The full
  guidance is injected by graft's own `SessionStart` hook.
- **A new or changed rule goes in ONE module**, with a link from here if it binds every task. A
  second detailed copy of a rule is a gate failure, and the gate names both files.

## 3. The routing table

Every module is `docs/agent/<id>.md`. Nothing below is loaded at startup: read the one your task
names. `topics` is what `--topic` matches; each module also declares the file globs it governs, and
the router resolves those for you rather than making you read the table.

**Acquiring and running somebody else's program**

| module | the authority on | topics |
| --- | --- | --- |
| `aucom.extractor-acquisition` | The extractor is acquired, never embedded — catalogue, updates and the compatibility manifest | extractor, aue, acquisition, download, update, catalogue, manifest, signature, licence |
| `aucom.extractor-execution` | Running it — the handshake, offline authority, bounded invocation, and what authorization is not | extractor, handshake, protocol, offline, bounds, timeout, authorization, verification |
| `aucom.job-bounds` | A bound is not a measurement, and the bound comes from here | job, bounds, retention, logbuf, memory, disk, stress, limits |

**Profiles, approvals and trust**

| module | the authority on | topics |
| --- | --- | --- |
| `aucom.export-gate` | The export gate is the only way out, and trust does not travel in | publish, export, profile, portable, trust, community, install, deployment |
| `aucom.approvals` | There is one writer of an approval, and it refuses | approval, grant, binding, digest, review, cli-parity |
| `aucom.threat-model` | The threat model is code, and there is one writer of a mutable local file | threat, security, risk, matrix, review-date, lockfile, mitigation, adr |

**Game families and joining**

| module | the authority on | topics |
| --- | --- | --- |
| `aucom.quake2-maturity` | Quake II is work in progress, and no document may say otherwise | quake2, maturity, work-in-progress, engine-family, capability, toolchain |
| `aucom.quake3-maturity` | Quake III is work in progress too, and it is not Quake II renumbered | quake3, q3map2, maturity, shader, patch, toolchain, dependencies |
| `aucom.join-readiness` | Joining is one readiness model, and a link starts nothing | join, game, live-game, readiness, ticket, stage, assetsync, uri, engine |

**Shipping it**

| module | the authority on | topics |
| --- | --- | --- |
| `aucom.release-verification` | A build is not a verification, and five states keep them apart | release, build, artifacts, native, windows, macos, matrix, bundle, acceptance |
| `aucom.launcher-merge` | It absorbed the Launcher (AUL), and there is one implementation of each thing | launcher, aul, merge, history, bootstrap, duplication |
| `aucom.component-addresses` | Component addresses live in `.env`, never in code | addresses, env, ports, localhost, port-contract, endpoint |
| `aucom.desktop-shell` | The desktop shell is deferred, and the browser is not a fallback for it | desktop, shell, wails, webview, window, native-ui, cgo |

## 4. Using the router

```sh
# what should I read for this task? Paths and reasons — never the contents.
../auto-pigeon-tools/scripts/agent_context_router.py --repo-root "$PWD" route \
    --prompt "$MAPPER_ROOT/LLM/prompts/auto-pigeon-companion/<prompt>.md"
../auto-pigeon-tools/scripts/agent_context_router.py --repo-root "$PWD" route --path internal/joinready/assess.go
../auto-pigeon-tools/scripts/agent_context_router.py --repo-root "$PWD" route --topic windows --topic release

# every module, its authority, its topics and the globs it governs
../auto-pigeon-tools/scripts/agent_context_router.py --repo-root "$PWD" modules

# the budget gate, and the accounting for this file's split
../auto-pigeon-tools/scripts/agent_context_router.py --repo-root "$PWD" gate
../auto-pigeon-tools/scripts/agent_context_router.py --repo-root "$PWD" sections | grep '5\.'
```

A prompt may name modules itself with an optional `context_docs:` list in its frontmatter — module
ids or module paths — read by the one frontmatter decoder, so a prompt without it reads exactly as
it did before.

**A comment elsewhere in this repository that cites `AGENTS.md §2` or `§3d` is still right, and
`sections` is what resolves it.** The module bodies keep their original heading lines verbatim, so
`sections | grep '5\.'` answers *which file* that section now lives in, in one command. Nothing was
renumbered.

The router names files and reasons and **never concatenates them**; its budgets are engineering
budgets over the words an agent loads before anybody types anything. And no `@import` may be added
to `AGENTS.md` or `CLAUDE.md` to "link" a module: `@` is EAGER, so it would put that module's whole
text back in the startup set, and the gate fails it. `auto-pigeon-tools/docs/agent/README.md` is
the authority on the module schema and the route shape; `docs/agent/README.md` here says what is
specific to this repository.
