# Vendored: Impeccable (impeccable.style)

Third-party design skill for coding agents by Paul Bakaus, **Apache-2.0** (`LICENSE`, `NOTICE.md` here).
Not first-party Auto-Pigeon code.

- Upstream: https://github.com/pbakaus/impeccable
- Pinned at tag `engine-v0.1.22`, commit `90b3072f0f7c9a7a85f64d6dd3db9cd94d364eff` (skill `version: 4.8.2`; its launcher pins engine
  `0.1.21` and verifies every download against `scripts/engine-checksums.txt`).
- Copied verbatim: `.claude/skills/impeccable/` and `.claude/agents/impeccable-*.md`. Added here:
  `LICENSE`, `NOTICE.md` (upstream's) and this file.
- Hooks (HITL 2026-11-11: skill + hooks, no `/impeccable init`): upstream's `SessionStart`,
  `PostToolUse` (`Edit|Write`) and `Stop` commands are merged into `.claude/settings.json`. Each is a
  no-op when `scripts/impeccable` is absent. On first use the launcher downloads the platform engine binary
  into `~/.impeccable/` (or `$IMPECCABLE_HOME`); set `IMPECCABLE_BIN` to a preinstalled binary to avoid it.
  Runtime state (`scripts/bin/`, `scripts/config.json`, `.impeccable/` caches) is gitignored.

Update: replace this directory and the agents with a newer upstream tag's copies, keep these three files,
and re-check `.claude/settings.json` against upstream's `.claude/settings.json`.
