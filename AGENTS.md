# AGENTS.md — Auto-Pigeon Launcher

## 0. What this repository is

This repository is **Auto-Pigeon Launcher**, abbreviated **AUL**. Its expected
location is:

```text
mapper-code/auto-pigeon-launcher/
```

It is one of the repositories under `mapper-code/`, and it follows the
same mapper-wide prompt/handoff workflow as every sibling. See
`README.md` for what the program itself does — this file covers only how
agents work in it.

This file was created to carry the shared workflow rules, which this repo
previously had nowhere to record. It deliberately does **not** invent
engineering rules for this codebase; add those in a task that is actually
about them.

## Cross-repository boundary

Agents may inspect sibling repositories and run their public CLI/API when
a prompt needs it. Do not edit, stage, commit, or push a sibling
repository unless the prompt explicitly names it as a mutation target.

When a task adds or changes a user-visible feature, update `README.md` in
the same task. New or changed CLI/API behavior requires an executable
example.

## Prompt queue and handoff workflow

See `$MAPPER_ROOT/LLM/WORKFLOW.md` for the full mapper-wide protocol
(prompt/handoff layout, how the next prompt is selected, prerequisite
declarations, manual-work handoffs, the end-of-task marker). This repo
follows it exactly; the items below are what's specific to working in
*this* repo.

- Prompts live in `$MAPPER_ROOT/LLM/prompts/auto-pigeon-launcher/`,
  handoffs in `$MAPPER_ROOT/LLM/handoffs/auto-pigeon-launcher/`.
- New prompts: name them `YYYYMMDD_NN_Title-Case-With-Dashes.md` per the
  shared convention, and give them both a `## Prerequisite` prose section
  and a `requires:` YAML frontmatter block (`requires: []` if none). The
  one prompt already in the archive predates that convention and has no
  `NN`; per `WORKFLOW.md`, its marker uses the whole filename stem.
- Session start: before resolving the next prompt, check for unrecorded
  manual work (see WORKFLOW.md) and write a `MANUAL_*` handoff first if
  any exists.
- End of every task: print `WORKFLOW.md`'s end-of-task marker as the
  literal last line of the final response. **Unconditionally** — every
  status, and however the session was started (`run-agent.sh`,
  `run-sequence.sh`, or a bare `claude` typed here). If nothing injected
  the prompt number, derive it: alias from `.agent-repo.json`, `NN` from
  the prompt filename. If there is no prompt file at all, print the
  manual-work form. `WORKFLOW.md` has the format and the derivation
  steps; do not restate the format here.
- `agent_task.py checkpoint` writes are not fully trusted once more than
  one prompt might be outstanding -- verify the resulting handoff's
  `prompt_path` before finishing; write by hand if it's wrong (see
  WORKFLOW.md for why).

This repo is **not** routable through `run-agent.sh` — it has no entry in
that script's alias table. Sessions here are started by hand, which makes
the unconditional marker rule above the only thing that identifies them.

## Session continuity and context-budget safety

Same rules as every other repo under `mapper-code/` — see
`$MAPPER_ROOT/LLM/WORKFLOW.md` and `CLAUDE.md` in this repo. Create the
task's canonical handoff near the start with status `in_progress`,
refresh it after each coherent phase, and never rely on chat or terminal
scrollback to preserve anything decisive.

## Component addresses live in `.env`, never in code

**No component may compile in where another component lives.** Not as a constant, not as a
fallback, not as a "last resort" default, not in a test fixture that production code reads. No
`localhost`, no `127.0.0.1`, no `192.168.*`, no port number standing in for a service.

### Why this is a rule and not a preference

`20260807_02` was reported like this: *"'Active Sessions' sends me to `http://127.0.0.1:5174/` even
though in auto-pigeon `.env` there is `AUTO_PIGEON_GALLERY_BASE_URL=http://192.168.0.33:5174/`."*
Both halves were true at once. Two things had gone wrong and each was invisible on its own:

1. a **duplicate** `AUTO_PIGEON_GALLERY_BASE_URL` line had been appended below the hand-written one,
   and a `.env` is *sourced*, so the last line silently won;
2. `launch-aup.sh` set AUG's address in AUP's sibling but never set AUP's copy of AUG's — the link
   was wired in one direction only.

Neither would have reached a user if the code had had no opinion about where AUG was. Instead a
compiled-in `127.0.0.1:5174` turned a misconfiguration into a plausible-looking wrong answer, on a
LAN, where a loopback address means *the reader's own machine* and can never work. A missing address
that says so gets fixed in an afternoon; a wrong address that looks right gets reported three times.

### What to do instead

- **Add a variable to that component's `.env` / `.env.example`, with a comment saying what reads it.**
- **Test it**, by running the thing and watching it use the configured value.
- **Then document it** in this file and in the component's `README.md`.
- **If you cannot put it in `.env` — stop and ask the human.** Do not invent a fallback address to
  keep moving.
- Whoever brings the stack up wires **both** directions. `launch-aup.sh` is the one place that knows
  the ports that were actually claimed, so it is the one place that writes them into each `.env`.

### The one permitted derivation

A page may use **its own origin** — `location.hostname`, `location.protocol`. That is not a
hardcoded location, it is the single address the reader is already known to be able to reach, and it
is the right answer whenever two services are served from one host. A **port** cannot be derived
that way, so an origin that needs a port needs a variable.

### When the address is missing

Say so, in the place the user is. An unconfigured address is an error the user must act on — in AUP
that means a modal per `DESIGN.md` §10, naming the variable — never a silent fallback and never a
button that goes somewhere wrong.
