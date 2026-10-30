# ADR-0011: map conversion is an open-source converter built here

Status: accepted, 2026-10-30 (`NEW_371`, operator decision). Supersedes the converter path of
ADR-0008's bundled extractor, and the operator's 2026-10-02 instruction to build the latest extractor
for every release, for this path only.

## Context

A build needs one thing from the extractor: an APMap turned into a `.map`. For that the Companion bundled
a proprietary program of about 29 MB, and a public repository needed a private checkout and a token
(`AUE_CHECKOUT_TOKEN`) to publish a release. Nobody could build this project's releases from its source.

## Decision

- The format code moved to `go/mapio` in the public `auto-pigeon-libraries`, Apache-2.0, by its owner.
  The extractor imports it too, so there is one implementation.
- This repository builds `auto-pigeon-mapio` from `cmd/mapio` (MIT) and that library. It ships beside the
  Companion as a separate process, with the same verification, bounds and provenance the extractor had,
  under its own identity and licences.
- The library revision is one line of `go.mod`. No workflow resolves a branch, and no job reads a secret.
- There is no fallback to the extractor, and `AUCOM_AUE_BINARY` is never read as a path.

## Consequences

- A release builds from a clone. `build/aue-pin.json`, the `aue` job and `AUE_CHECKOUT_TOKEN` are gone from
  the release path; deleting the repository secret is the operator's act.
- A new APMap version reaches releases through a deliberate `go get`, not automatically from the head of
  another repository. The protection the 2026-10-02 instruction gave now depends on that step.
- `go.mod` gains a requirement, and only `cmd/mapio` links it, so the Companion's own binary still links no
  external module.
- The bundle carries one notices file. The 2026-09-25 rule against licence files in an archive is narrowed
  for it, because Apache-2.0 requires the text to travel with the library in binary form.
- Manifests move to `aucom.release-manifest/2.0` and `aucom.bundle-manifest/2.0`. A Companion that bundled the
  extractor refuses them. Accepting them would have needed a manifest calling the converter the extractor, and
  that record would have been false. See the amendment below.
- Old release assets, and old build and Play records that say `extractor`, are history and stay as they are.

## Amendment: the way across (`NEW_371A`, 2026-10-30)

"Its user downloads one archive by hand" was a material tradeoff recorded as if it were a compatibility result.
The evidence is now: no change to a reader can reach an installed program; the released v1.336 refuses a 2.0
release (`manifest_schema_unsupported`); it would take a bundle in its own shape whose version it can reach.

So a **migration bridge** exists as a prepared artifact, classified separately from a release. It carries the
Companion and converter with the authentic historical extractor pinned by digest, under true names and licences,
in the older manifests, with a `migration_bridge` member saying what it is. The unmodified v1.336 took one and the
next ordinary update removed the extractor, on a local transport with the real bytes. The operator may instead
keep the manual route (`README.md`), which is tested too and is what users have until a bridge is published.

- A bridge never becomes a normal dependency: no workflow builds one, the release plan refuses one, and normal CI
  stays free of the extractor.
- Its reachability is bounded and has to be planned: an older Companion reads six manifests, so once six releases
  have a higher version it finds nothing. `build/migration-bridge.py plan` answers this for a given release list,
  and a later bridge needs a fresh version in a gap between releases.
- The carried extractor is proprietary, so publishing one is a licensing act as well as a release act. It stays the
  operator's decision. `aucom.managed-tools` rule 13 holds the rules.
