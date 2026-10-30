#!/usr/bin/env python3
"""Write one platform bundle's manifest, and place the converter in it.

The manifest maps EVERY member of the bundle to product, version, platform,
size and SHA-256. Built by walking the directory rather than from a list
somebody maintained beside it: a manifest assembled from a list is a manifest
that is wrong the first time a file is added and nobody updates it.

The converter rules, in one place:

  * The converter is auto-pigeon-mapio BUILT BEFOREHAND by build/release.sh, from
    cmd/mapio in this commit and the go/mapio revision go.mod pins, and passed in
    with --converter. Nothing here downloads anything.
  * It is copied in as `dependencies/auto-pigeon-mapio[.exe]`, a SEPARATE FILE in
    the folder beside the Companion, which is where the Companion looks for it and
    checks its digest against this manifest (also in `dependencies/`). Never
    linked, never inside the Companion's binary. On macOS "beside" is inside the
    .app, in `Contents/MacOS/`, and the manifest is in `Contents/Resources/`
    (releaselib.layout).
  * It must be built for THIS platform, and so must the Companion: both headers
    are read and a disagreement is refused (releaselib.platform_of).
  * The licences are stated, never implied. The converter is Apache-2.0 AND MIT:
    the go/mapio library and the wrapper. MIT alone is refused, because an archive
    listing it so would read as entirely MIT.
  * Its notices file travels with it, and is the one licence text a bundle
    carries. No other licence or notice file ships in an archive (operator
    decision, 2026-09-25; narrowed for this file by NEW_371, because the library is
    redistributed in binary form and Apache-2.0 asks for its text and NOTICE).
  * The licences list separates what an archive holds: the Companion's own code
    (MIT), the auto-pigeon-libraries contract files compiled into the Companion
    (Apache-2.0), and the converter.
  * With no --converter the bundle is complete and carries no converter, and says
    so; the Companion then reports that map conversion is unavailable.
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

SCHEMA = "aucom.bundle-manifest/2.0"
FULL_SHA = re.compile(r"^[0-9a-f]{40}$")
LIBRARY_MODULE = "github.com/auto-pigeon/auto-pigeon-libraries"


def place_converter(args, bundle, where):
    """Copy the prebuilt converter and its notices into the bundle."""
    if not args.converter_version:
        raise SystemExit("error: --converter needs --converter-version: a bundle must say which build it carries")
    if not args.converter_source:
        raise SystemExit(
            "error: --converter needs --converter-source: a bundle must say which repository and commit "
            "the converter it carries came from"
        )
    spdx = args.converter_spdx.strip()
    if spdx.upper().startswith("MIT") or "APACHE-2.0" not in spdx.upper():
        raise SystemExit(
            f"error: --converter-spdx {args.converter_spdx!r}: the converter is Apache-2.0 AND MIT. It links an "
            "Apache-2.0 library, and an archive that listed it under MIT alone would read as entirely MIT"
        )
    for name, value in (("--converter-commit", args.converter_commit), ("--library-commit", args.library_commit)):
        if not FULL_SHA.match(value or ""):
            raise SystemExit(
                f"error: {name} {value!r} is not a full commit SHA; the bundle must name the one commit "
                "the converter was built from"
            )
    if not args.library_version:
        raise SystemExit("error: --converter needs --library-version: the go/mapio revision compiled into it")
    if not os.path.isfile(args.converter):
        raise SystemExit(f"error: the converter {args.converter} is not a file")
    if not os.path.isfile(args.converter_notices or ""):
        raise SystemExit(f"error: the converter's notices {args.converter_notices!r} are not a file; its licences "
                         "require them to travel with it")
    try:
        built_for = releaselib.platform_of(args.converter)
    except ValueError as err:
        raise SystemExit(f"error: the converter: {err}")
    if built_for != args.platform:
        raise SystemExit(
            f"error: the converter {args.converter} is built for {built_for} and this bundle is {args.platform}; "
            "a bundle pairs the two programs for ONE machine"
        )

    target = os.path.join(bundle, where["converter"])
    os.makedirs(os.path.dirname(target), exist_ok=True)
    shutil.copyfile(args.converter, target)
    os.chmod(target, 0o755)
    notices = os.path.join(bundle, where["notices"])
    os.makedirs(os.path.dirname(notices), exist_ok=True)
    shutil.copyfile(args.converter_notices, notices)
    os.chmod(notices, 0o644)
    return {
        "product": releaselib.CONVERTER,
        "version": args.converter_version,
        "platform": args.platform,
        "file": where["converter"],
        "notices": where["notices"],
        "license": spdx,
        "source_commit": args.converter_commit,
        "source": args.converter_source,
        "library": {
            "module": LIBRARY_MODULE,
            "version": args.library_version,
            "commit": args.library_commit,
            "license": "Apache-2.0",
        },
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--platform", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--bundle", required=True)
    parser.add_argument("--converter", default="", help="a prebuilt auto-pigeon-mapio for this platform")
    parser.add_argument("--converter-version", default="")
    parser.add_argument("--converter-source", default="", help="the repository and commit it came from")
    parser.add_argument("--converter-spdx", default="Apache-2.0 AND MIT")
    parser.add_argument("--converter-commit", default="", help="the full commit SHA it was built from")
    parser.add_argument("--converter-notices", default="", help="the notices file that travels with it")
    parser.add_argument("--library-version", default="", help="the go/mapio module version compiled in")
    parser.add_argument("--library-commit", default="", help="that version's full commit SHA")
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

    sidecar = place_converter(args, args.bundle, where) if args.converter else None

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
            if sidecar and relative in (sidecar["file"], sidecar["notices"]):
                product, version = sidecar["product"], sidecar["version"]
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
        "converter": sidecar,
    }
    if sidecar is None:
        manifest["converter_absent"] = {
            "reason": "This bundle was assembled without an auto-pigeon-mapio build.",
            "consequence": (
                "APMap conversion is unavailable in this bundle. The Companion downloads no program: a "
                "converter reaches it only in a release bundle, beside it, or through the "
                "AUCOM_MAPIO_BINARY developer override, which is labelled unverified."
            ),
        }
    else:
        manifest["licenses"].append(
            {
                "product": sidecar["product"],
                "spdx": sidecar["license"],
                "notices": sidecar["notices"],
                "covers": (
                    "the converter executable beside the Companion: go/mapio from auto-pigeon-libraries "
                    "(Apache-2.0) and cmd/mapio from this repository (MIT); neither licence relicenses the other part"
                ),
            }
        )

    # On macOS the manifest goes in Contents/Resources/, beside the converter's
    # notices.
    os.makedirs(os.path.dirname(manifest_path), exist_ok=True)
    with open(manifest_path, "w", encoding="utf-8") as handle:
        json.dump(manifest, handle, indent=2, sort_keys=False)
        handle.write("\n")
    if sidecar is None:
        print(f"bundle {args.platform}: no converter inside")
    else:
        print(f"bundle {args.platform}: converter {sidecar['version']} beside the Companion as {sidecar['file']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
