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
  * It is copied in as `auto-pigeon-extractor[.exe]`, a SEPARATE FILE beside
    the Companion, which is where the Companion looks for it and checks its
    digest against this manifest. Never linked, never inside the Companion's
    binary. On macOS "beside" is inside the .app, in `Contents/MacOS/`, and the
    manifest is in `Contents/Resources/` (releaselib.layout).
  * It must be built for THIS platform, and so must the Companion: both
    headers are read and a disagreement is refused (releaselib.platform_of).
  * It carries its own licence file, whatever the extractor's repository ships
    (--extractor-license, --extractor-spdx), and the manifest records the exact
    commit it was built from. The two programs are the same owner's; the
    Companion is MIT and the extractor's terms are its own (operator,
    2026-09-23: "don't worry too much about licensing"). Nothing here publishes
    the extractor's source.
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

SCHEMA = "aucom.bundle-manifest/1.1"
COMPANION_LICENSE = "LICENSE-auto-pigeon-companion.txt"
EXTRACTOR_LICENSE = "LICENSE-auto-pigeon-extractor.txt"
FULL_SHA = re.compile(r"^[0-9a-f]{40}$")


def place_extractor(args, bundle, where):
    """Copy the prebuilt extractor and its licence file into the bundle."""
    if not args.extractor_version:
        raise SystemExit("error: --extractor needs --extractor-version: a bundle must say which build it carries")
    if not args.extractor_source:
        raise SystemExit(
            "error: --extractor needs --extractor-source: a bundle must say which repository and commit "
            "the extractor it carries came from"
        )
    if not args.extractor_license or not os.path.isfile(args.extractor_license):
        raise SystemExit(
            "error: --extractor needs --extractor-license, the extractor's own licence file: the bundle "
            "carries two programs and must say whose terms each is under"
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
    shutil.copyfile(args.extractor, target)
    os.chmod(target, 0o755)
    destinations = [bundle]
    if where["resources"]:
        # A .app dragged to /Applications leaves the archive root behind; the
        # licence travels inside it too.
        destinations.append(os.path.join(bundle, where["resources"]))
    for directory in destinations:
        shutil.copyfile(args.extractor_license, os.path.join(directory, EXTRACTOR_LICENSE))
    return {
        "product": "auto-pigeon-extractor",
        "version": args.extractor_version,
        "platform": args.platform,
        "file": where["extractor"],
        "license": args.extractor_spdx,
        "license_file": EXTRACTOR_LICENSE,
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
    parser.add_argument("--extractor-license", default="", help="the extractor's own LICENSE file")
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
            {"product": "auto-pigeon-companion", "spdx": "MIT", "file": COMPANION_LICENSE},
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
            {"product": "auto-pigeon-extractor", "spdx": sidecar["license"], "file": EXTRACTOR_LICENSE}
        )

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
