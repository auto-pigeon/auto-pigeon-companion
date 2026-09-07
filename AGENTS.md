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

## 2. THE EXPORT GATE IS THE ONLY WAY OUT, AND TRUST DOES NOT TRAVEL IN (`AUB/AUP/AUG/AUCOM 213`)

`AUCOM 204` defined the portable profile and `212` made one runnable. `20260906_213` made one
publishable. `internal/publish` owns both directions; the README's *Publishing a profile, and taking
somebody else's* section is the user-facing statement of them. Six things follow, and they bind every
future prompt.

1. **THERE IS ONE FUNCTION THAT PRODUCES BYTES TO PUBLISH, AND IT REFUSES.** `publish.PreviewOf`
   calls `profile.Export`, which validates and canonicalizes, and canonicalization runs
   `CheckPortable`. So a document naming an absolute path, a home directory, a network address or
   anything shaped like a credential cannot be PREVIEWED — let alone published. That is what makes
   "publishing a local binding is structurally impossible" a property of the code rather than a rule
   somebody remembers. **Never add a second path to publishable bytes**, and never add a `--force`,
   an `--allow-local` or a "publish this document as-is".

2. **PUBLISHING NEEDS AN EXPLICIT CONFIRMATION AND INSTALLING NEEDS AN EXPLICIT APPROVAL.** Both are
   parameters, not defaults: `Publish` refuses `confirmed: false` and `Apply` refuses
   `approved: false` before writing ANYTHING — not even the document, because a document on disk is
   one the local catalog lists, and listing something nobody agreed to is how a review becomes a
   formality.

3. **A DEPLOYMENT'S TRUST STATE NEVER BECOMES THIS MACHINE'S.** An installed profile is
   `profile.TrustCommunity`, always. AUB's `builtin` is the ONE state `profile.Authorize` accepts
   with no grant, and AUB's `verified` claims a catalogue signature this build did not check;
   adopting either would let anybody who runs an AUB hand out an unreviewed run.
   `TestADeploymentCannotHandOutTrustThisMachineDidNotCheck` fails a change that does. The badge is
   carried as `Plan.DeploymentTrust` and shown, because it is real information about what somebody
   with a stake in that deployment thinks — it is just not this machine's authorization.

4. **THE DIGEST IS RECOMPUTED HERE AND THE CANONICAL FORM IS CHECKED HERE.** AUB deliberately does
   not re-canonicalize, because RFC 8785 is this repository's algorithm; so this is the side that
   proves it, by re-exporting the decoded document and comparing byte-for-byte. Never accept the
   announced digest, and never install bytes that are not the canonical encoding of what they decode
   to — a digest over a non-canonical encoding names bytes nobody else would produce for the same
   document.

5. **THE PORTABILITY CORPUS IS SHARED AND PINNED.** `internal/profile/testdata/portability-corpus.json`
   is byte-identical to auto-pigeon-backend's copy and both repositories pin its SHA-256, because
   neither can read the other's tree at test time. Changing what may never be published is a change
   in two repositories, in one task, or in neither.

6. **A WITHDRAWN VERSION IS INSTALLABLE, AND NEVER SILENTLY.** Reproducing a build that used one is a
   legitimate reason to want it. The publisher's reason and any replacement they named travel with
   the plan and are printed before an approval. Never refuse one, and never install one without
   printing why it was withdrawn.

**What must not be done to make something pass.** Do not add a flag that skips the preview or the
approval. Do not map AUB's trust word onto `profile.Trust`. Do not trust an announced digest. Do not
publish anything but `profile.Export`'s output. Do not send a `trust`, `moderation_state`, `verified`
or `publisher` member in a publication — they do not exist in the request and this client must not
grow them.

## 3. QUAKE II IS WORK IN PROGRESS, AND NO DOCUMENT MAY SAY OTHERWISE (`AUP/AUCOM 215`)

`20260906_215` added a Quake II path that works and is not finished, and the
second half of that sentence is the part with machinery behind it.

**The statement lives in `internal/maturity`, keyed on AUB's `engine_family`.**
It is not a member of a profile document, and it must not become one. A profile
is written by whoever publishes it; the first community Quake II toolchain to
declare itself stable would be a community document switching off a warning this
build stands behind. `maturity.Of(family)` is what *this build* says, a
community document is subject to it exactly as a built-in one is, and every
surface — CLI listings, `profile show`, `build preview`, a running build, the
Build/Run/Profiles areas, AUP's own panes — renders the same
`maturity.Quake2Message`. Its SHA-256 is pinned here and in AUP's
`frontend/src/services/gameMaturity.ts`, the way the portability corpus is,
because neither repository can read the other at test time.

**The two toolchains have different capability ids on purpose.** `q1.bsp.compile`
and `q2.bsp.compile`. A shared `bsp.compile` would make "which compiler runs
this step" a question a pipeline answers by iteration order, and the wrong
answer produces a Quake 1 BSP for a Quake II project.
`TestAQuake2PipelineCannotResolveAgainstTheQuake1Toolchain` checks both
directions. Never merge them.

**Everything the Q2 documents claim about ericw-tools 2.x was measured**, by
running 2.0.0-alpha7 against a synthetic Quake II map. Seven behaviours differ
from the qualified Q1 line and each is written into the document with its
reason: no `bin/` in the archive, `maputil`, `<stem>-vis.log` / `<stem>-light.log`
written *beside the input*, the `<stem>.texinfo.json` that `light` reads back,
`-lit` doing nothing, `bspinfo` writing files, and contents coming from the
`.wal` rather than from the texture name. A future change that re-derives a Q2
document from the Q1 one is undoing that.

**The base game data root is required, and that follows from the last of those.**
A Quake II compile with nothing bound at `game_root` exits 0 and writes a BSP
with default flags, no playerclip and no sky. Making the binding optional would
trade a refusal that names the binding for a map that looks built and is wrong.
Three roots, bound separately: `tool_root`, `game_root` (`-basedir`),
`content_root` (`-gamedir`).

**An engine profile declares the actions upstream documents and no others.**
FTEQW's Quake II profile has `join_server` alone, because upstream's own
QuickStart says every local Quake II server needs gamecode FTEQW does not ship
and Auto-Pigeon must not distribute. Yamagi uses `-datadir` and `+set game`, not
`-basedir` and `-game`, because Yamagi's own filesystem source calls the latter
deprecated. Adding an action to make a button appear is the failure mode here.

**`internal/feedback` builds a report; it does not filter one.** There is no
member of `feedback.Report` that can hold a map, a log or a path, so nothing has
to be cleaned up. `Consent` is four booleans defaulting to none, and the report
records what was chosen so "did not share" and "shared, and there was none" stay
distinguishable. A diagnostic carries the *profile's declared* message, never
the tool's line. A credential or a path is **refused and named**, using
`profile.CheckPortable` and `job.Redactor` as detectors rather than as filters —
quietly redacting would hand a user a document they believe they wrote. Nothing
in this repository sends a report anywhere, and adding a destination is a
decision about somebody else's data, not a convenience.

## 4. QUAKE III IS WORK IN PROGRESS TOO, AND IT IS NOT QUAKE II RENUMBERED (`AUP/AUCOM 216`)

`20260906_216` added a Quake III path on the pattern `215` established, and the
places where it is deliberately NOT the same pattern are the ones that bind a
future prompt.

**Its sentence is its own.** `maturity.Quake3Message` names shader, patch and
entity workflows because a Quake III map has a shader script and patches; the
Quake II sentence does not, because a Quake II map has neither. Both are pinned
here and in AUP's `frontend/src/services/gameMaturity.ts`, and
`TestTheTwoSentencesSayDifferentThings` fails a change that derives one from the
other. Adding a family is one row in one table on each side — every surface
already reads it, and a surface that had to be told about a new game is a
surface that would one day not be told.

**There is no managed download for Q3Map2, and that is the decision, not a gap.**
Upstream's Linux release is a `.7z` holding one AppImage of the whole NetRadiant
editor; Windows is a 43 MB zip of the same; macOS has nothing; and `q3map2`
resolves libassimp, libdraco, libminizip and libicu out of the bundle's own
`../lib`, so there is no smaller artifact to prefer. `internal/acquire` unpacks
zip and tar.gz. A prompt that adds a `managed_download` here has to add a
catalogue entry for a map editor first, and `TestQ3Map2DeclaresNoManagedDownloadAndSaysWhy`
is where it is asked to think about that.

**Q3Map2 is one program with three stage switches**, where the EricW documents
are several programs. Its capability ids are `q3.bsp.compile`, `q3.bsp.vis` and
`q3.bsp.light` — a third set, not a merge — and
`TestAPipelineCannotResolveAgainstAnotherGamesToolchain` now checks six pairs
rather than two, because "it is all the same compiler anyway" is exactly the
reasoning that would produce a Quake 1 BSP for a Quake III project.

**Nine measured behaviours are written into the document, and each differs from
both EricW toolchains.** There is no output argument at all — the BSP, portal
and surface files appear beside the input, so every output is declared with
`in_place` and an extension rather than a path. `-vis` needs the `.prt` and
nothing else; `-light` refuses to start without BOTH `<stem>.srf` and
`<stem>.map`, which is why the pipeline wires the map source into the lighting
step as well as the compile. `-vis` without `-saveprt` deletes the portal file it
was handed, so that switch is not an option. A leak exits 0 having written no
BSP, so it is the missing required output that fails the job. A missing texture
is a warning and exit 0. Lighting is nondeterministic above one thread. A
re-derivation of the Q3 document from the Q1 or Q2 one is undoing all of that.

**`internal/q3deps` reviews; it does not filter, and it does not repair.** It
resolves what a map names against the archive about to be written, the user's
content and the base game, and `package create --map` refuses on it. Four rules
hold it up:

1. **It agrees with the compiler, and that is a test.**
   `testdata/q3map2-2.5.17n-report.txt` is what Q3Map2 actually printed for the
   fixture map; the scan's `missing` set has to equal the shaders it could not
   find an image for. A change to the parser that stops agreeing fails there.
2. **The base game's shader scripts are read, out of `pak0.pk3` as well as
   loose.** A review that called every `common/*` shader missing is a review a
   user learns to click past — and one nobody reads is worse than none. What a
   base-game shader pulls in is NOT followed: it is inside somebody else's PK3.
3. **The limits are a member of the report**, not a paragraph in a README. A
   model's internal references are not read, and the report says so where the
   model is listed. Do not remove a limit sentence; add one when you add a gap.
4. **`--accept-missing` requires `--reason`, prints the review first and prints
   the reason back.** It is not a way to switch the check off, and there must
   never be a flag that skips the review itself.

**A face or patch shader name carries no `textures/` prefix** — measured both
ways, including for `patchDef2`. The scan normalizes the way the compiler does
and names the doubling when it sees it, because `textures/textures/…` reads like
a missing file and is an authoring mistake.

**ioquake3 declares all five actions** where FTEQW's Quake II profile declares
one, and the difference is upstream's own download: `baseq3/vm/qagame.qvm` ships
beside the engine, so a local Quake III server needs no third-party gamecode.
What it still needs is `pak0.pk3`, which is id's. The generic id Tech 3 profile
sends only `fs_basepath`, `fs_game`, `sv_maxclients`, `map` and `connect`, and
has no dedicated-server action because the name of a dedicated binary is each
project's own invention.

**The compiler redirects `-fs_homepath` and the engines do not.** A build must
read only what was bound to it; a player's settings, demos and screenshots
belong where the engine puts them. Those are different requirements and must not
be made consistent with each other.

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
