---
id: aucom.approvals
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: There is one writer of an approval, and it refuses
authority:
  - aucom-approval-service
topics:
  - approval
  - grant
  - binding
  - digest
  - review
  - cli-parity
  - api
paths:
  - internal/approval/**
  - internal/binding/**
---

# There is one writer of an approval, and it refuses

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 447-499 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

## 6. THERE IS ONE WRITER OF AN APPROVAL, AND IT REFUSES (`AUCOM/AUT 228`)

`AUT/AUCOM 219` found the one place the README's *"everything the page can do,
the CLI can do too"* was false: `profile.NewGrant` had three call sites — engine
profiles only, a deployment listing only, and the local API — so a TOOL profile
somebody wrote and dropped into their profile folder could be validated, shown,
digested and bound from a terminal, and then only ever RUN by starting the GUI
server. `20260907_228` closed it, and what it left behind binds a later prompt.

**`internal/approval.Service` is the one writer of a `profile.Grant`.** The local
API holds one, `companion profile grant` holds one, and so does the approval half
of `engine bind` and of `POST /api/v1/profiles/{id}/bind`. Two implementations of
"record an approval" drift, and the one that drifts is the one that forgot a
check — so a new surface takes this service, and never assembles a
`binding.LocalBinding` with a `Grant` in it of its own. `publish/install.go` still
writes one for a document arriving from a deployment listing, against a plan the
user approved in the same call; that is the one remaining exception and it is
reviewed by `TestNothingIsWrittenWithoutAnApproval`.

**An approval names the exact document, and reviewing is not approving.**
`Service.Grant` refuses an empty digest (`ErrDigestRequired`) and a digest that is
not the document on this machine (`StaleDigestError`, which names both), BEFORE
writing anything — including the binding, because a binding written by a refused
approval is a state nobody asked for. `Service.Review` writes nothing at all, so
"show me what this asks for" is safe to type. `companion profile grant <id>` with
no decision prints the review and exits **2**: a script that left out `--approve`
must not be able to read that as an approval.

**The CLI contract is non-interactive and stays that way.** `--digest` says WHICH
document and `--approve` says a person decided; `--confirm` withdraws. There is no
prompt, because a prompt hangs automation, and a script that cannot approve a
profile is a script that has to start the GUI server — which is the browser-only
path this removed. Never add `--force`, `--yes`, or a flag that skips the review.

**Binding is not approving, and a pipeline grants nothing to its tools.** Import
writes a document; `acquire resolve --bind` and the setup form write where a
program is; none of them writes a grant.
`TestImportingAndBindingGrantNothing` and
`TestApprovingAPipelineGrantsNothingToTheToolsItRuns` are where that is checked.

**A changed document invalidates the grant even when it asks for LESS.** The
grant is against bytes. A build that compared permission sets would let a
narrower republication of the same version run unreviewed, which is why
`TestAChangedDocumentInvalidatesTheGrantEvenWhenItAsksForLess` asserts the
narrowness first and then the refusal.

**The catalogue's URL redaction went with the catalogue (2026-09-23).**
`catalog.redactField` and `RedactURL` were deleted when the Companion stopped
downloading programs. Addresses and secrets that reach a job's output are
redacted by `job.Redactor`; do not weaken it to tidy a message.
