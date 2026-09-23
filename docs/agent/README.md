# `docs/agent/` — the selectively-read agent modules

Every detailed rule this program has lives in exactly one file here. **Nothing in this directory is
loaded at startup.** The repository-root `AGENTS.md` holds the rules that bind nearly every task,
the source-of-truth order and the routing table;
`../../../auto-pigeon-tools/scripts/agent_context_router.py` resolves a task to the modules it
needs and answers with **paths and reasons, never contents**.

```sh
../auto-pigeon-tools/scripts/agent_context_router.py --repo-root "$PWD" route --topic join
../auto-pigeon-tools/scripts/agent_context_router.py --repo-root "$PWD" route --path internal/threat/matrix.go
../auto-pigeon-tools/scripts/agent_context_router.py --repo-root "$PWD" route \
    --prompt "$MAPPER_ROOT/LLM/prompts/auto-pigeon-companion/<prompt>.md"
../auto-pigeon-tools/scripts/agent_context_router.py --repo-root "$PWD" gate
../auto-pigeon-tools/scripts/agent_context_router.py --repo-root "$PWD" sections | grep '5\.'
```

## The architecture is not this repository's

`NEW_246B_AUT_Workspace-Agent-Documentation-Router-And-Budget-Gate` established it and
`auto-pigeon-tools/docs/agent/README.md` is the authority on the module schema
(`aut-agent-module/1`), the route shape, the reason ranking, the `context_docs:` prompt key and the
budgets. This file says what is specific to **auto-pigeon-companion**; it does not restate that
contract, for the same reason a module does not restate a workspace-root rule.

There is ONE `agent-context-manifest.json` and it lives in `auto-pigeon-tools`, because the
budgets, the eager-import allowlist and the repository roll-call are workspace facts. Each
repository splits its own `AGENTS.md` into its own `module_dir`, which is `docs/agent` here.

## Why the directory exists

`AGENTS.md` was 782 lines and 7,372 words, and an agent paid all of it before anybody typed
anything — the extractor acquisition chain and its handshake, the export gate's six rules, two
work-in-progress game families with their pinned sentences, the fifty-row threat matrix, the
approval writer, the retention bounds and the five native-support states — in every session,
whatever the task was. Adding `CLAUDE.md` the fixed cost was **7,954 words**, which is what `246G`'s
prompt measured.

The repair is not deletion. Every rule is still here, in one place, and `section-manifest.json`
proves it: every heading AND every LINE of the pre-split file is accounted for, and
`agent_context_router.py gate` re-derives that accounting on every run rather than trusting it.

| | before | after |
| --- | --- | --- |
| `AGENTS.md` | 782 lines / 7,372 words | see `gate --json` |
| `CLAUDE.md` | 90 lines / 582 words | see `gate --json` |
| fixed AUCOM startup (both files) | 7,954 words | see `gate --json` |
| routed, selectively read | 0 | 12 modules |

The "after" column is deliberately a command rather than a number: a figure written here would be a
description of the day it was written, which is the same reason the manifest holds no measurement.

## What the split preserved, exactly

- **Every stable section id still resolves.** The module bodies keep their **original heading lines
  verbatim**, so `## 5. THE THREAT MODEL IS CODE…` is still spelled that way and
  `sections | grep '5\.'` answers *which file* in one command.
- **The bytes are unchanged.** Every module body was copied out of the pre-split file by line range
  — not rewritten, not summarised, not reordered. The one exception is
  `aucom.component-addresses.md`, which is a POINTER: `diff` of that section against the
  workspace-root section of the same name reported **one difference, a trailing blank line**, so
  the module names the authority instead of keeping a second copy.
  That was true at the split. On 2026-09-23 the operator removed every download mechanism, and the
  prose under the extractor headings in `aucom.extractor-acquisition` and `aucom.extractor-execution`
  was rewritten to match — heading lines still verbatim, retired rules marked **Retired
  2026-09-23** rather than deleted, so a citation still lands somewhere that says what happened.
- **One section is split by LINE rather than by heading**, and the split falls on its own
  subsection boundary: §1's acquisition half (originally the catalogue, the update machinery and
  the compatibility manifest; since 2026-09-23 how the extractor ships beside the Companion and why
  nothing downloads it) is `aucom.extractor-acquisition`, and its execution half (the handshake,
  the invocation bounds, and the retired offline/authorization/publishing rules) is
  `aucom.extractor-execution`, which declares the first a prerequisite because a question about
  running an extractor is half answered without knowing how it got here.
- **The work-in-progress truth for Quake II and Quake III is preserved exactly**, in two modules
  rather than one, because `246G`'s prompt asked for it and because the pre-split file already made
  the point that the second is not the first renumbered. Both sentences remain pinned by SHA-256
  here and in AUP.

### `CLAUDE.md` changed in two small ways

It was already lean and Claude-specific. It gained the router call — a session that has resolved
its prompt should ask what to read before reading — and its `Context-budget and stale-session
rules` heading became **`Checkpoint discipline`**, with the ownership of the context lifecycle
stated: that belongs to `run-sequence.sh` through its `PreCompact` hook, and
`auto-pigeon-tools/tests/test_claude_md_context_rules.py` scans every repository here for a
self-estimated window or a percentage threshold coming back. What the section actually carried —
checkpoint early, refresh often, prefer a fresh session over a bare `continue` — is handoff
discipline and is kept.

## The module set

Twelve modules, grouped by what a task is about. The root routing table is the one-line-each
version; this is the shape of the set.

| group | modules |
| --- | --- |
| finding and running somebody else's program | `aucom.extractor-acquisition`, `aucom.extractor-execution`, `aucom.job-bounds` |
| profiles, approvals and trust | `aucom.export-gate`, `aucom.approvals`, `aucom.threat-model` |
| game families and joining | `aucom.quake2-maturity`, `aucom.quake3-maturity`, `aucom.join-readiness` |
| shipping it | `aucom.release-verification`, `aucom.launcher-merge`, `aucom.component-addresses` |

Two prerequisite edges are declared rather than left to a reader: `aucom.extractor-execution` pulls
`aucom.extractor-acquisition` for the reason above, and `aucom.quake3-maturity` pulls
`aucom.quake2-maturity` because the whole point of the Q3 section is the places where it is
deliberately *not* the Q2 pattern, and that reads as arbitrary without the pattern beside it.

## `README.md` is documentation, not instruction

This repository's `README.md` is the user-facing manual for the program: what it does, every
command, and how to publish or install a profile. No `SessionStart` hook loads it, no `@import`
reaches it, and neither `AGENTS.md` nor `CLAUDE.md` asks for it to be read whole. It is classified
as **public / user documentation** and deliberately stays out of fixed context and out of
`docs/agent/`.

Two rules follow. The README remains the user-facing statement of the rules the modules own —
`AGENTS.md`'s README rule is unchanged, and a task that adds a user-visible feature still updates
it in the same task, including the CLI/page parity claim `aucom.approvals` had to repair once. And
it must not be turned into an instruction source: no auto-loaded file may `@import` it, and a
module that needs a README section states the rule itself rather than telling an agent to go and
read it whole. It was **not split**, because churning a stable public document that nothing loads
would cost review time and save no context at all.

`docs/adr/` is the decision record and is routed by the module whose subject it belongs to —
`0007` by `aucom.threat-model`. An ADR is evidence for a rule, not a second copy of it.
