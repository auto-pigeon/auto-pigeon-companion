---
id: aucom.component-addresses
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: Component addresses live in .env, never in code
authority:
  - aucom.component-addresses
topics:
  - addresses
  - env
  - configuration
  - ports
  - localhost
  - port-contract
  - endpoint
authority_elsewhere:
  - component-addresses-in-env
  - port-contract-9190
---

# Component addresses live in .env, never in code

<!-- REPLACES AGENTS.md line(s) 739-782 (sha256 dc16c6c72b64a661) by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization. Not moved verbatim, deliberately: those forty-four lines were a second copy of a workspace-root section differing only in a trailing blank line, and this module is a POINTER at that authority. -->

This rule is **not restated here**, and that is the repair rather than an omission.

`AGENTS.md`'s `Component addresses live in .env, never in code` section held it as a second copy of
the workspace-root `mapper-code/AGENTS.md` section of the same name. Measured by
`NEW_246G_AUCOM`: `diff` of the two reported **one difference, a trailing blank line**. Every word
of the prohibition, the `20260807_02` incident it came from, the `.env` remedy, the stop-and-ask,
the one permitted derivation and what a missing address must say was already in the authority,
unchanged.

The authority is:

```text
mapper-code/AGENTS.md  ##  Component addresses live in `.env`, never in code
mapper-code/AGENTS.md  ##  The AUB port contract — 9190
```

**Every AUCOM session already loads both.** Claude Code resolves `CLAUDE.md` up the directory
hierarchy, `mapper-code/CLAUDE.md` is `@AGENTS.md` on line 1, and `AUT 246A` proved both edges for
this repository. Codex discovers the same ancestor `AGENTS.md` by its own documented rule. So the
rule is in front of every agent working here whether or not this repository repeats it, and the
only thing repeating it bought was a second place for it to drift.

What binds an AUCOM change is therefore unchanged, and the repository-root `AGENTS.md` states it in
one line: **no component may compile in where another component lives**. This program is the one
that runs on somebody else's machine, so it bites in a particular way here: AUB's address, a
deployment's listing URL and a host's endpoint are configuration and a user's own input — never a
constant, never a fallback, and never a fixture production code reads. A published profile that
names a network address does not even reach a preview (`CheckPortable`), which is the same rule
enforced one layer down.

The **AUB port contract** is in the same workspace-root file: `9190` is the host-facing AUB
endpoint, `8666` is AUB's container port, and `8090` is **PocketBase's** framework default and
never Auto-Pigeon's. Never report `8090` as a running AUB endpoint; runtime truth comes from the
`launch-aup` resolved summary, then `.env` / `/api/status`, then the actual listening socket — never
a compiled fallback and never a string a UI printed.

**Do not copy either section back into this repository.**
`../auto-pigeon-tools/scripts/agent_context_router.py gate --repo-root "$PWD"` fails a change that
declares one stable rule id authoritative in two live files, and this pointer is what keeps that
check honest for this rule.
