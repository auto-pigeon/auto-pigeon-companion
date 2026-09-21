#!/usr/bin/env python3
"""Write one platform bundle's manifest, and fetch the pinned extractor.

The manifest maps EVERY member of the bundle to product, version, platform,
size and SHA-256. Built by walking the directory rather than from a list
somebody maintained beside it: a manifest assembled from a list is a manifest
that is wrong the first time a file is added and nobody updates it.

The sidecar rules, in one place:

  * A pinned extractor is fetched from the EXACT url the pin names, and its
    bytes must hash to the EXACT digest the pin names. A mismatch is an error
    and nothing is written; there is no flag that accepts it.
  * With no pin the bundle is complete and carries no extractor, and says so.
    The Companion then obtains one against the signed catalogue, verified, as a
    managed install — the same route it has always had.
  * The extractor is a SEPARATE FILE beside the Companion, never renamed,
    never linked, never inside it.

`AUCOM/AUE/AUT 246I1`.
"""

import argparse
import hashlib
import json
import os
import sys
import urllib.request

SCHEMA = "aucom.bundle-manifest/1.0"


def digest_of(path):
    sha = hashlib.sha256()
    size = 0
    with open(path, "rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            sha.update(block)
            size += len(block)
    return size, "sha256:" + sha.hexdigest()


def fetch_sidecar(pin, platform, bundle):
    """Download the pinned extractor for this platform, verified."""
    if not pin.get("enabled"):
        return None, pin.get("why_disabled", "")
    version = pin.get("version", "").strip()
    base = pin.get("release_base_url", "").strip()
    if not version or not base:
        raise SystemExit(
            "error: the sidecar pin is enabled and names no version or no release url. "
            "A pin without both is not a pin."
        )
    if "latest" in base:
        raise SystemExit(
            "error: the sidecar pin's release url contains `latest`. An asset behind a "
            "mutable url is an asset nobody can state the contents of in advance."
        )
    for artifact in pin.get("artifacts", []):
        if artifact.get("platform") != platform:
            continue
        name = artifact["file"]
        expected = artifact["sha256"]
        url = base.rstrip("/") + "/" + name
        target = os.path.join(bundle, name)
        with urllib.request.urlopen(url) as response, open(target, "wb") as out:
            while True:
                block = response.read(1 << 20)
                if not block:
                    break
                out.write(block)
        _, actual = digest_of(target)
        if actual != expected:
            os.remove(target)
            raise SystemExit(
                f"error: {name} hashes to {actual} and the pin says {expected}. "
                "Nothing is bundled."
            )
        return {
            "product": "auto-pigeon-extractor",
            "version": version,
            "platform": platform,
            "file": name,
            "license": "AGPL-3.0-or-later",
            "corresponding_source": pin.get("corresponding_source", ""),
            "verified": "the pin's sha256, checked against the downloaded bytes",
        }, ""
    raise SystemExit(f"error: the sidecar pin is enabled and names no artifact for {platform}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--platform", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--bundle", required=True)
    parser.add_argument("--pin", required=True)
    args = parser.parse_args()

    with open(args.pin, encoding="utf-8") as handle:
        pin = json.load(handle)

    sidecar, why_absent = fetch_sidecar(pin, args.platform, args.bundle)

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
            "reason": why_absent,
            "how_it_is_obtained": (
                "Auto-Pigeon Companion downloads the extractor against a signed catalogue, at the "
                "version a signed compatibility manifest names, verifies its digest and its "
                "protocol handshake, and records it as a managed install. That is one of exactly "
                "two ways this program will ever reach an extractor; the other is an explicit "
                "local developer override, which is labelled unverified wherever it is shown."
            ),
        }
    else:
        manifest["licenses"].append(
            {"product": "auto-pigeon-extractor", "spdx": "AGPL-3.0-or-later",
             "corresponding_source": sidecar["corresponding_source"]}
        )

    out = os.path.join(args.bundle, "bundle-manifest.json")
    with open(out, "w", encoding="utf-8") as handle:
        json.dump(manifest, handle, indent=2, sort_keys=False)
        handle.write("\n")
    print(f"== {out} ({len(members)} member(s), extractor: "
          f"{sidecar['version'] if sidecar else 'not bundled'})", file=sys.stderr)


if __name__ == "__main__":
    main()
