#!/usr/bin/env python3
"""Write one platform bundle's manifest, and place the extractor in it.

The manifest maps EVERY member of the bundle to product, version, platform,
size and SHA-256. Built by walking the directory rather than from a list
somebody maintained beside it: a manifest assembled from a list is a manifest
that is wrong the first time a file is added and nobody updates it.

The extractor rules, in one place:

  * The extractor is an Auto-Pigeon Extractor binary BUILT BEFOREHAND — by the
    release workflow from the extractor's own repository at the commit
    `build/aue-pin.json` names, or by hand — and passed in with --extractor.
    Nothing here downloads anything.
  * It is copied in as `dependencies/auto-pigeon-extractor[.exe]`, a SEPARATE
    FILE in the folder beside the Companion, which is where the Companion looks
    for it and checks its digest against this manifest (also in
    `dependencies/`). Never linked, never inside the Companion's
    binary. On macOS "beside" is inside the .app, in `Contents/MacOS/`, and the
    manifest is in `Contents/Resources/` (releaselib.layout).
  * It must be built for THIS platform, and so must the Companion: both
    headers are read and a disagreement is refused (releaselib.platform_of).
  * The manifest records the licence identifier the extractor's own release
    manifest declares (--extractor-spdx) and the exact commit it was built
    from. Both are QUOTED from the extractor build, never restated here, so a bundle cannot disagree with
    the program inside it. The Companion is MIT; the extractor is proprietary
    (LicenseRef-Auto-Pigeon-Proprietary, NEW_247G), and a build distributed
    earlier under AGPL-3.0-only keeps that licence. The one identifier refused
    is MIT: the extractor was never MIT, and an archive listing it so would read
    as entirely MIT. Nothing here publishes the extractor's source.
  * The licences list separates the three things an archive holds: the
    Companion's own code (MIT), the auto-pigeon-libraries contract files
    compiled into the Companion (Apache-2.0), and the extractor (its own).
    It names identifiers, not files: no licence or notice file ships in the
    archive (operator decision, 2026-09-25). The texts are in the repositories.
  * With no --extractor the bundle is complete and carries no extractor, and
    says so; the Companion then reports that map inspection is unavailable.
"""

import argparse
import json
import os
import re
import shutil
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
# No __pycache__ beside the scripts: an untracked file in the checkout makes the
# next `go build` stamp vcs.modified=true, which the archive check refuses.
sys.dont_write_bytecode = True
import releaselib  # noqa: E402

SCHEMA = "aucom.bundle-manifest/1.2"
FULL_SHA = re.compile(r"^[0-9a-f]{40}$")


def place_extractor(args, bundle, where):
    """Copy the prebuilt extractor into the bundle."""
    if not args.extractor_version:
        raise SystemExit("error: --extractor needs --extractor-version: a bundle must say which build it carries")
    if not args.extractor_source:
        raise SystemExit(
            "error: --extractor needs --extractor-source: a bundle must say which repository and commit "
            "the extractor it carries came from"
        )
    if args.extractor_spdx.strip().upper().startswith("MIT"):
        raise SystemExit(
            f"error: --extractor-spdx {args.extractor_spdx!r}: the extractor is not MIT. MIT is the Companion's "
            "licence; an archive that listed the extractor under it would read as entirely MIT"
        )
    if not FULL_SHA.match(args.extractor_commit or ""):
        raise SystemExit(
            f"error: --extractor-commit {args.extractor_commit!r} is not a full commit SHA; the bundle "
            "must name the one commit the extractor was built from"
        )
    if not os.path.isfile(args.extractor):
        raise SystemExit(f"error: the extractor {args.extractor} is not a file")
    try:
        built_for = releaselib.platform_of(args.extractor)
    except ValueError as err:
        raise SystemExit(f"error: the extractor: {err}")
    if built_for != args.platform:
        raise SystemExit(
            f"error: the extractor {args.extractor} is built for {built_for} and this bundle is {args.platform}; "
            "a bundle pairs the two programs for ONE machine"
        )

    target = os.path.join(bundle, where["extractor"])
    os.makedirs(os.path.dirname(target), exist_ok=True)
    shutil.copyfile(args.extractor, target)
    os.chmod(target, 0o755)
    return {
        "product": "auto-pigeon-extractor",
        "version": args.extractor_version,
        "platform": args.platform,
        "file": where["extractor"],
        "license": args.extractor_spdx,
        "source_commit": args.extractor_commit,
        "source": args.extractor_source,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--platform", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--bundle", required=True)
    parser.add_argument("--extractor", default="", help="a prebuilt auto-pigeon-extractor for this platform")
    parser.add_argument("--extractor-version", default="")
    parser.add_argument("--extractor-source", default="", help="the repository and commit it came from")
    parser.add_argument("--extractor-spdx", default="LicenseRef-auto-pigeon-extractor",
                        help="what the extractor's release manifest says its licence is")
    parser.add_argument("--extractor-commit", default="", help="the full commit SHA it was built from")
    args = parser.parse_args()

    try:
        where = releaselib.layout(args.bundle, args.platform)
        companion = os.path.join(args.bundle, where["companion"])
        if not os.path.isfile(companion):
            raise ValueError(f"{companion} is not there; the bundle has no Companion")
        companion_platform = releaselib.platform_of(companion)
    except ValueError as err:
        raise SystemExit(f"error: {err}")
    if companion_platform != args.platform:
        raise SystemExit(
            f"error: the Companion in {args.bundle} is built for {companion_platform} and this bundle is "
            f"{args.platform}"
        )

    sidecar = place_extractor(args, args.bundle, where) if args.extractor else None

    manifest_path = os.path.join(args.bundle, where["manifest"])
    members = []
    for root, _, names in os.walk(args.bundle):
        for name in sorted(names):
            path = os.path.join(root, name)
            if os.path.abspath(path) == os.path.abspath(manifest_path):
                continue
            relative = os.path.relpath(path, args.bundle).replace(os.sep, "/")
            size, sha = releaselib.digest_of(path)
            product = "auto-pigeon-companion"
            version = args.version
            if sidecar and relative == sidecar["file"]:
                product, version = "auto-pigeon-extractor", sidecar["version"]
            members.append(
                {
                    "path": relative,
                    "product": product,
                    "version": version,
                    "platform": args.platform,
                    "size": size,
                    "sha256": sha,
                }
            )
    members.sort(key=lambda member: member["path"])

    manifest = {
        "schema_version": SCHEMA,
        "product": "auto-pigeon-companion",
        "version": args.version,
        "platform": args.platform,
        "companion": where["companion"],
        "members": members,
        "licenses": [
            {
                "product": "auto-pigeon-companion",
                "spdx": "MIT",
                "covers": "the Companion's own code only",
            },
            {
                "product": "auto-pigeon-libraries contract files compiled into the Companion",
                "spdx": "Apache-2.0",
                "license_url": "https://www.apache.org/licenses/LICENSE-2.0",
                "covers": "@auto-pigeon/incident-contract and @auto-pigeon/operational-notice-contract, unmodified",
            },
        ],
        "extractor": sidecar,
    }
    if sidecar is None:
        manifest["extractor_absent"] = {
            "reason": "This bundle was assembled without an Auto-Pigeon Extractor build.",
            "consequence": (
                "Map inspection and APMap conversion are unavailable in this bundle. The Companion "
                "downloads no program: an extractor reaches it only in a release bundle, beside it, "
                "or through the AUCOM_AUE_BINARY developer override, which is labelled unverified."
            ),
        }
    else:
        manifest["licenses"].append(
            {
                "product": "auto-pigeon-extractor",
                "spdx": sidecar["license"],
                "covers": "the extractor executable beside the Companion; not covered by the Companion's MIT licence",
            }
        )

    # On macOS the manifest goes in Contents/Resources/, which nothing else
    # populates now that no licence file ships there.
    os.makedirs(os.path.dirname(manifest_path), exist_ok=True)
    with open(manifest_path, "w", encoding="utf-8") as handle:
        json.dump(manifest, handle, indent=2, sort_keys=False)
        handle.write("\n")
    if sidecar is None:
        print(f"bundle {args.platform}: no extractor inside")
    else:
        print(f"bundle {args.platform}: extractor {sidecar['version']} beside the Companion as {sidecar['file']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
