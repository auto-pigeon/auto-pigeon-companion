---
title: "NEW_348 audit C5-C12, N1-N4, f14b: owners, live before/after, traps"
author: Andrea-dintino
date: 2026-10-24
branch: main
cost: { minutes: 139, tokens: 986050 }
touches:
  - internal/build/mapconfine_test.go
---

## Decided
- C11: the idempotency fingerprint is operation + REQUEST PATH + body for every keyed sharedworkspace route (claimKey scope); replays render key.SubjectID under their binding. Pre-upgrade rows retried across the upgrade answer conflict.
- C7/C8: decide on the RESOLVED collection (FindCachedCollectionByNameOrId), never a literal path; the reset captcha/limiter run in a router middleware BEFORE PocketBase's lookup (its record hook never fires for an unknown address).
- C6: an account that redeemed a bound beta invitation needs `verified` even with AUB_MAIL_VERIFICATION_REQUIRED=false (mailgate.InstallBindingProof); AUB's own GET /verify/email-change bypasses PB's confirm hook, so it takes mail.ConfirmGuard.
- C5: the remote result's expanded ceiling IS AUE_OUTPUT_BYTES (NormalizeFleet), no new env var; a size refusal removes output/ + capture/ only.
- AUCOM: map-reference confinement lives in build.stageInputs for inputs WITH a SourceRef (AUB-sourced); user-chosen local maps untouched. The local page refuses a positively-identified other OS account per connection (ConnContext; /proc/net/tcp on Linux).
- Docs pages self-host the integrity-checked Stoplight bundle under a strict CSP.
## Tried and ruled out
- Opening the pre-fix /docs in Chrome would contact unpkg: intercept `*unpkg.com*` with CDP Fetch and fulfil a local substitute instead.
- A Companion binary built before a code change silently runs the old code (gave an accidental, useful in-product "before"); rebuild before each live phase.
- `pkill -f "<binary> serve"` kills your own shell (the pattern is in its command line): anchor with `^<abs path>`.
- HTTP checks on a `--bare` stack fail with legal_acceptance_required until each account accepts the documents; signups need `legal_acceptance`.
## Watch out
- internal/auejob panics under load in executeRemote after teardown (backlog P2) — also on pristine HEAD; not a regression signal.
- ericw parse_token: `\"` followed by a line break CLOSES the quote (old-editor "c:\foo\"); mirror that or you refuse valid maps.
- AUP's same-name Save As hits the game-profile mismatch refusal before the workspace guard when profiles differ — use a map with the same profile to exercise C9 in the UI.
- PocketBase sends the password-reset mail fire-and-forget: assert it with a bounded wait.
