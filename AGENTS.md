# AGENTS.md — Auto-Pigeon Companion

## 0. What this repository is

This repository is **Auto-Pigeon Companion**, abbreviated **AUCOM**. Its expected
location is:

```text
mapper-code/auto-pigeon-companion/
```

It is one of the repositories under `mapper-code/`, and it follows the
same mapper-wide prompt/handoff workflow as every sibling. See
`README.md` for what the program itself does — this file covers only how
agents work in it.

This file was created to carry the shared workflow rules, which this repo
previously had nowhere to record. It deliberately does **not** invent
engineering rules for this codebase; add those in a task that is actually
about them.

### It absorbed the Launcher

`auto-pigeon-launcher` (**AUL**) was a second, overlapping bootstrap of the
same program. In `20260906_203` its history was merged into this
repository and it was retired: `mapper-code/auto-pigeon-launcher/` still
exists, still has all its branches, tags and source, and now carries only
a deprecation notice pointing here. Nothing was deleted or archived
remotely.

What that means for work here:

- AUL's commits are reachable from this repository's `main` through the
  merge commit. `git log` covers both.
- There is one implementation of each thing. If you find yourself adding
  a second auth, config, CLI or web stack "for the launcher case", the
  merge is being undone.
- Do not mutate `auto-pigeon-launcher/` unless a prompt names it as a
  mutation target, exactly as for any other sibling.

## 1. THE EXTRACTOR IS NOT IN THIS BINARY, AND THERE IS NO THIRD WAY TO ONE (`AUE/AUB/AUCOM 211`)

**Auto-Pigeon Extractor is a separate program under a different licence. It is
downloaded against a signed catalogue, at a version a signed compatibility
manifest names, and run as its own process. Nothing here contains it, embeds it,
or claims a licence over it.**

```text
managed             a verified cache entry, at the version the manifest names
developer override  AUCOM_AUE_BINARY, unverified, local, and labelled so
```

There is **no third way and no fallback between the two.** A managed resolution
that fails is an error the user reads; it never quietly becomes an override, and
an override is never quietly treated as verified.
`internal/aue.Provenance.Verified` is the ONE place that distinction is
recorded, it travels with every runner, and every surface that shows an
extractor shows it. A caller that has to compute "was this verified" from four
other fields is a caller that will one day compute it wrong.

### What embedding cost, and why it is three separate faults

`internal/aue/embed.go` copied a platform's extractor binary into this executable
with `//go:embed`. It was never released, and it had to go for three unrelated
reasons — a later change that fixes one of them has not fixed the others:

1. **Licensing.** An MIT artifact contained and appeared to cover an AGPL
   program, and a user had no way to tell whose bytes they were running.
2. **Verification.** Nothing checked the staged binary. `//go:embed` resolves at
   compile time, so a stale or wrong-platform file shipped silently and failed
   on the user's machine.
3. **Coupling.** A patched extractor needed a new Companion release.

CI fails if it returns: `no-embedded-extractor` checks for the directory, for
the directive, and for any committed executable anywhere in the tree.

### It reuses the acquisition machinery; it is not a second updater

`internal/acquire` verifies, downloads, caches and re-checks — the same verifier,
the same sticky revocations, the same serial ratchet, the same cache every other
managed tool uses. This package decides only WHICH version to ask for. A change
to the verification chain therefore applies here with nothing kept in step, and
a prompt that adds a second update path for the extractor is undoing that.

### The compatibility manifest is a THIRD signed document, and it carries no digest

`aucom.compatibility/1.0` maps a Companion version and a platform to a component
version and a minimum protocol. Every fact about the BYTES — size, digest,
signer, licence, source — stays in the catalogue and only there, so there is no
second copy of a digest for a careless edit to make wrong. Two documents that
both carry the digest can disagree; one carries the digest and the other carries
the choice.

**Overlapping rules are refused, not resolved by order.** A rule that depends on
which one a reader's eye reaches first is a rule nobody can review, and a
publisher who splits a range and gets the boundary wrong by one release would
silently install the older build for everybody in the overlap. `Requirement`
therefore returns *the* match rather than the first, and a later change that
introduced a precedence order would have to delete that check first.

### The protocol handshake, before the executable is used for anything

A verified executable is not automatically one this build can talk to. **Majors
equal, minor at least the required one** — never `>= major`, because a later
major is defined as breaking. The rule lives in one function on each side of the
boundary (`catalog.ProtocolSatisfies` here, `protocol.Satisfies` there) and a
second call site implementing a laxer version of it is the failure mode.

Without `min_protocol` the Companion would be trusting a version NUMBER to imply
a contract, which is exactly the assumption a rebuilt or forked extractor breaks.

### Offline is a different authority, never a weaker check

`extractor-pin.json` records the requirement this machine last VERIFIED, beside
`config.json` with the catalogue state because it records a decision and
clearing a cache must not erase one. Offline resolution reads it and the
handshake still runs against the minimum it carries.

**The fallback happens only because the caller said `Offline`.** A verification
that failed, a rollback attempt, an expired document or an unreachable server is
a refusal and never becomes "use the older answer" — that is the difference
between an offline mode and a way around the catalogue.

### Every invocation is bounded, and the bounds are not decoration

A timeout on the WHOLE run, because a process printing one line every nine
minutes keeps a per-read deadline satisfied for ever. **SIGTERM first**, because
the extractor's published contract says a supervised run ends deliberately on
one and writes a record saying why; the grace period is a field, so the default
is the patient production answer and a test can shorten it. A fresh working
directory per invocation, removed afterwards. A bounded read, because a
subprocess is not a trusted producer of unbounded output — and the cap is
reported as the cap, not as whatever the child died of when the pipe closed.

`RunJSON` refuses an empty body and trailing content. A subprocess can exit 0
having printed a warning, half a document, or a document with something
appended, and each of those decodes into a partially-filled struct a caller then
acts on.

### Authorization is not verification

When the artifact is served by a backend that authorizes downloads,
`acquire.Options.Authorize` rewrites the URL immediately before the fetch.
Nothing else changes: the size, the digest and the signature chain are checked
exactly as they are for a public URL. **Who let you fetch the bytes and whether
the bytes are the right bytes are different questions with different answers**,
and keeping them apart is what stops a compromised backend from being able to
make this program run something.

`Downloader` still holds no credentials and sends none. What the hook returns is
a capability for one artifact valid for minutes — the pre-signed-URL shape
`catalog.RedactURL` already exists to keep out of logs and records.

### Publishing is executable, and holds no key here

`companion catalog release` turns a component's release manifest into the two
unsigned documents, validated against the rules a signature would otherwise make
permanent — including the copyleft rule that refuses a package offering no
corresponding source. `companion catalog sign` is a separate step, so a
publisher reads what they are about to vouch for. **CI reads no secrets**, and a
job of its own keeps it that way: a workflow that could sign from a pull request
would be a workflow that publishes whatever a pull request contains.

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

- Prompts live in `$MAPPER_ROOT/LLM/prompts/auto-pigeon-companion/`,
  handoffs in `$MAPPER_ROOT/LLM/handoffs/auto-pigeon-companion/`.
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

This repo **is** routable through `run-agent.sh`. That script no longer
recites an alias table: it discovers one from every checkout's
`.agent-repo.json`, so `./run-agent.sh claude AUCOM` works, and
`./run-agent.sh --repos` lists `AUCOM auto-pigeon-companion` alongside its
siblings. Verify with that command rather than trusting this sentence.

Sessions here are still often started by hand — `run-sequence.sh`, or a
bare `claude` typed in this directory — and a hand-started session gets no
task context: the only `SessionStart` hook this repo has is graft's, which
refreshes the code graph and injects a repository map, not a prompt or a
handoff. So the unconditional marker rule above holds whichever way the
session began, and so does resolving the task yourself when nothing
resolved it for you.

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
