---
id: aucom.launcher-merge
schema: aut-agent-module/1
repository: auto-pigeon-companion
title: It absorbed the Launcher (AUL), and there is one implementation of each thing
authority:
  - aucom-launcher-merge
topics:
  - launcher
  - aul
  - merge
  - history
  - bootstrap
  - duplication
---

# It absorbed the Launcher (AUL), and there is one implementation of each thing

<!-- Moved verbatim from AGENTS.md by NEW_246G_AUT_AUG_AUCOM_AUTEL_AULIBS_Remaining-Repository-Documentation-Modularization: line(s) 36-54 of the AGENTS.md at sha256 dc16c6c72b64a661. This module is the ONE authoritative home for the rules below; the repository-root AGENTS.md routes to it and keeps no second copy. -->

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
