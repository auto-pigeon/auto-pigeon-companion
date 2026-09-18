@AGENTS.md

# Claude Code delivery rules

`AGENTS.md` above holds this repository's always-rules, its source-of-truth order and the routing
table for `docs/agent/`. Claude Code must follow it. What is below is the part that is specifically
about how a Claude Code session here reports its work — it is not repeated in `AGENTS.md`, and
`AGENTS.md`'s rules are not repeated here.

This repository is routable through `run-agent.sh` — discovery reads the
`AUCOM` alias out of `.agent-repo.json`, and `./run-agent.sh --repos`
shows it. A session started that way arrives with its task resolved.

A session started any other way does not. The only `SessionStart` hook
here is graft's, which injects a repository map and no task context at
all, so **when nothing resolved the task for you, resolve it yourself**:
read the newest handoff in
`$MAPPER_ROOT/LLM/handoffs/auto-pigeon-companion/`, then the prompt it
points at, then the next prompt in
`$MAPPER_ROOT/LLM/prompts/auto-pigeon-companion/`.

Then ask the router what this task should read, rather than reading everything:

```sh
../auto-pigeon-tools/scripts/agent_context_router.py --repo-root "$PWD" route --prompt <the resolved prompt path>
```

It answers with module paths and the reason each was selected. It never
prints a module's contents, and reading a routed module is your decision.

The terminal is a work surface, not the user handoff. Do not leave any
substantive completion information only in tool output, command output,
background-task output, progress text, or terminal scrollback.

Before ending any non-trivial task:

1. Create or update the canonical markdown handoff under
   `$MAPPER_ROOT/LLM/handoffs/auto-pigeon-companion/` with
   `../scripts/agent_task.py checkpoint`.
2. Reconcile it against the original request, every command result, every
   subagent result, `git diff --stat`, and `git status --short`.
3. Put the substantive handoff in the final assistant response shown to
   the user. Do not respond only with a path, a terse summary, or "see
   terminal."

The final assistant response must contain:

- overall status: complete, partial, or blocked;
- a requirement-by-requirement completion matrix;
- files added, modified, deleted, and intentionally untouched;
- implementation and design decisions;
- exact commands, exit status, and pass/fail/skip/timeout totals;
- build, lint, and manual-review results;
- blockers, caveats, incomplete work, and disproven hypotheses;
- branch, commit SHA and message, push result, and final
  `git status --short`;
- canonical handoff path;
- the complete `Next Recommended Task` section required by `AGENTS.md`;
- the end-of-task marker, as the literal last line (see below).

Do not claim completion until the handoff and final assistant response are
complete.

# End-of-task marker

The last line of the final assistant response is always `WORKFLOW.md`'s
end-of-task marker, `I <STATUS> PROMPT <N> ON: AUCOM`. Nothing goes
after it.

This is how the user tells which prompt an agent executed and in which
repo, at a glance, without opening the handoff — so it is required for
**every** task, whatever the status, and **however the session was
started**. It is never conditional on a router or a `SessionStart` hook
having injected context.

Work the number out: `<ALIAS>` is `alias` in this repo's
`.agent-repo.json` (`AUCOM`); `<N>` is the `NN` in the prompt's
`YYYYMMDD_NN_Title.md` filename under
`$MAPPER_ROOT/LLM/prompts/auto-pigeon-companion/`, or the whole filename
stem when the name carries no number; `<STATUS>` follows the handoff just
written. If the task had no prompt file at all, print the manual-work
form (`I COMPLETED MANUAL WORK ON: AUCOM`) rather than nothing.
`WORKFLOW.md` has the full status vocabulary and alias table.

# Checkpoint discipline

Create the canonical handoff near the start of every non-trivial task with
status `in_progress`. Refresh it after each coherent phase and before any
long-running command, background wait, or likely interruption. Make every
checkpoint sufficient for a new session to continue without replaying the
transcript. When resuming after an idle period or a usage-window reset,
prefer a fresh session that reads the newest handoff over a bare
"continue" into a stale one.

**Context lifecycle belongs to `run-sequence.sh`, not to you** —
`AGENTS.md` §2. Do not work out how full your window is, do not adopt a
threshold, do not compact by hand, and do not stop an unattended run to
ask for a session reset. The discipline above is about the handoff, which
is a deliverable; it is not a context policy.

# Compact instructions

When compacting, preserve only: the exact task objective and requirement
status; accepted design decisions; files changed and their material
state; decisive command/test results and unresolved failures; current Git
state; the canonical handoff path; the exact next executable step.
Discard verbose command output, repeated exploration, superseded
hypotheses, and conversational narration already captured in the handoff.
