#!/usr/bin/env python3
"""Write one platform bundle's manifest, and place the extractor in it.

The manifest maps EVERY member of the bundle to product, version, platform,
size and SHA-256. Built by walking the directory rather than from a list
somebody maintained beside it: a manifest assembled from a list is a manifest
that is wrong the first time a file is added and nobody updates it.

The extractor rules, in one place:

  * The extractor is an Auto-Pigeon Extractor binary BUILT BEFOREHAND — by the
    release workflow from the extractor's own repository, or by hand — and
    passed in with --extractor. Nothing here downloads anything.
  * It is copied in as `auto-pigeon-extractor[.exe]`, a SEPARATE FILE beside
    the Companion, which is where the Companion looks for it and checks its
    digest against this manifest. Never linked, never inside the Companion.
  * With no --extractor the bundle is complete and carries no extractor, and
    says so; the Companion then reports that map inspection is unavailable.
"""

import argparse
import hashlib
import json
import os
import shutil
import sys

SCHEMA = "aucom.bundle-manifest/1.0"
EXTRACTOR_SPDX = "AGPL-3.0-only"


def digest_of(path):
    sha = hashlib.sha256()
    size = 0
    with open(path, "rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            sha.update(block)
            size += len(block)
    return size, "sha256:" + sha.hexdigest()


def place_extractor(source, version, platform, bundle, corresponding_source):
    """Copy the prebuilt extractor into the bundle under the name the Companion reads."""
    if not version:
        raise SystemExit("error: --extractor needs --extractor-version: a bundle must say which build it carries")
    if not corresponding_source:
        raise SystemExit(
            "error: --extractor needs --extractor-source: the extractor is AGPL-3.0, and a bundle that "
            "carries it must say where its corresponding source is"
        )
    if not os.path.isfile(source):
        raise SystemExit(f"error: the extractor {source} is not a file")
    name = "auto-pigeon-extractor" + (".exe" if platform.startswith("windows-") else "")
    target = os.path.join(bundle, name)
    shutil.copyfile(source, target)
    os.chmod(target, 0o755)
    return {
        "product": "auto-pigeon-extractor",
        "version": version,
        "platform": platform,
        "file": name,
        "license": EXTRACTOR_SPDX,
        "corresponding_source": corresponding_source,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--platform", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--bundle", required=True)
    parser.add_argument("--extractor", default="", help="a prebuilt auto-pigeon-extractor for this platform")
    parser.add_argument("--extractor-version", default="")
    parser.add_argument("--extractor-source", default="", help="where its corresponding source is")
    args = parser.parse_args()

    sidecar = None
    if args.extractor:
        sidecar = place_extractor(args.extractor, args.extractor_version, args.platform, args.bundle,
                                  args.extractor_source)

    members = []
    for root, _, names in os.walk(args.bundle):
        for name in sorted(names):
            if name == "bundle-manifest.json":
                continue
            path = os.path.join(root, name)
            relative = os.path.relpath(path, args.bundle).replace(os.sep, "/")
            size, sha = digest_of(path)
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
        "members": members,
        "licenses": [
            {"product": "auto-pigeon-companion", "spdx": "MIT",
             "file": "LICENSE-auto-pigeon-companion.txt"},
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
            {"product": "auto-pigeon-extractor", "spdx": EXTRACTOR_SPDX,
             "corresponding_source": sidecar["corresponding_source"]}
        )

    out = os.path.join(args.bundle, "bundle-manifest.json")
    with open(out, "w", encoding="utf-8") as handle:
        json.dump(manifest, handle, indent=2, sort_keys=False)
        handle.write("\n")
    if sidecar is None:
        print(f"bundle {args.platform}: no extractor inside")
    else:
        print(f"bundle {args.platform}: extractor {sidecar['version']} beside the Companion as {sidecar['file']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
