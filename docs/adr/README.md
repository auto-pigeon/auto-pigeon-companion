# Auto-Pigeon Companion architecture decision records

Decisions that constrain how this repository is built, and that a later change
should have to argue with rather than simply overwrite. They describe what was
decided, not what someone would prefer.

Workspace-wide decisions — ones that constrain AUP, AUB, AUC, AUG or AUE as well
— live in the shared record set under `mapper/LLM/docs/adr/` and are not
duplicated here. The records in this directory are the Companion's own.

## Format

```markdown
# ADR-NNNN: <Title>

- Status: Proposed | Accepted | Deprecated | Superseded
- Date: YYYY-MM-DD
- Affected components: AUCOM
- Decision owners: <the prompt or person that settled it>

## Context
## Decision
## Consequences
### Positive
### Negative
### Risks
## Alternatives considered
## Evidence
## Follow-up work
```

Numbers are allocated monotonically and accepted records are never renumbered.
When a decision is replaced, mark the old record `Superseded`, and link both
ways.

## Index

| Number | Title | Status | Date |
|---|---|---|---|
| [0001](0001-profiles-are-data-and-the-executor-is-the-only-thing-that-runs.md) | Profiles are data, and the executor is the only thing that runs | Accepted | 2026-09-06 |
| [0002](0002-portable-profiles-and-local-bindings-are-different-types.md) | A portable profile and a local binding are different types in different packages | Accepted | 2026-09-06 |
| [0003](0003-one-executor-and-the-record-is-what-says-a-job-ran.md) | One executor, and the record — not a process — is what says a job ran | Accepted | 2026-09-06 |
| [0004](0004-acquisition-is-verified-or-it-does-not-happen.md) | Acquisition is verified, or it does not happen | Superseded by 0008 | 2026-09-07 |
| [0005](0005-a-pipeline-is-several-jobs-and-a-manifest-is-what-says-so.md) | A pipeline is several jobs, and a manifest is what says so | Accepted | 2026-09-07 |
| [0006](0006-provenance-decides-what-is-packaged-not-filenames.md) | Provenance decides what is packaged, and filenames decide nothing | Accepted | 2026-09-07 |
| [0007](0007-the-threat-model-is-code-and-one-writer-owns-mutable-state.md) | The threat model is code, and one writer owns every mutable local file | Accepted | 2026-09-08 |
| [0008](0008-the-companion-downloads-no-program.md) | The Companion downloads no program, and the extractor ships beside it | Accepted | 2026-09-23 |
