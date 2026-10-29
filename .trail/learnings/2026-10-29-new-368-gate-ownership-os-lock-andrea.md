---
title: "NEW_368 gate ownership: OS-lock guard + gate id for tooluse switch gates"
author: Andrea-dintino
date: 2026-10-29
branch: main
---

## Decided
- `internal/tooluse` switch gates: every publish / stale reclaim / heartbeat / release runs under the tools folder's guard = OS lock (flock; LockFileEx via kernel32 lazy proc on Windows, no dependency) on `<tools>/.in-use/guard.lock`, which is NEVER removed. Held for a few file ops only (bounded 10 s), never through a build/download/wait.
- Each gate record has an immutable `id`; `Gate.Release`/`beat` act only if the record at the path carries it (`whileOwned`). `Gate.Held()` exists so `managedtools.Manager.switchGate` re-checks a cached gate before trusting it.
- Unreadable gate = held until it is `staleAfter` old by mtime, then reclaimable (bounded recovery, never delete on parse failure alone).
- Leases need no guard: unique nonce paths nobody else writes. `switching()` reads unguarded and takes the guard only to reclaim.

## Tried and ruled out
- Re-reading before rename/unlink (the old `breakRecord` rename-aside) — still removes whatever occupies the path after the judgement; the second reclaimer deleted a third party's live gate (reproduced with `beforeReclaim` barrier, logs in `$MAPPER_ROOT/LLM/runs/auto-pigeon/NEW_368_Gate-Ownership-Race-Continuation/`).
- An O_EXCL lock FILE as the guard: needs its own stale-breaking, i.e. the same bug one level up.

## Watch out
- Barrier tests must work on both protocols: pause B inside reclamation, then `select` on "A finished" vs `guardContended` (A waiting for B's guard) before resuming B — otherwise the fixed code deadlocks the test.
- `internal/lockfile.breakStale` has the same rename-whatever-is-there pattern (two stale breakers vs a new holder); not fixed by NEW_368 — separate task.
- Companions ≤ v1.325 don't take the guard; the race remains possible only between an old and a new process during an upgrade.
