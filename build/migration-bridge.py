#!/usr/bin/env python3
"""Prepare a migration bridge: the one Companion an older updater can still take.

A Companion that bundled the extractor reads only aucom.release-manifest/1.0 and
aucom.bundle-manifest/1.2, and a release since NEW_371 is neither. A changed reader
cannot reach a program that is already installed, so an installed Companion has to be
offered something IT can read. That is a bridge (ADR-0011): this Companion and its
converter, in the older shape, with the authentic historical extractor beside them,
because the older reader will not take a bundle without one.

It is not a release, and nothing here makes it one:

  * No workflow runs this, and it needs no credential and no other repository.
  * It reads local files and downloads nothing. The historical archive comes from the
    published release it is pinned to in build/migration-bridge.json, and every input
    is compared with those pins.
  * It says what it is. The extractor is listed as the extractor, under its own
    licence, and the converter as the converter. A `migration_bridge` member in both
    manifests and NOTICES-migration-bridge.txt say why both are there and that nothing
    runs the extractor. Calling the converter the extractor would have been shorter
    and false.
  * Publishing one is a reviewed decision (`NEW_371A`). `plan` says whether a version
    can work at all; it publishes nothing.

macOS is refused: a macOS application is replaced by hand, so an updater never takes a
bundle there.

  migration-bridge.py build --version <v> --commit <sha> --platform <p> \\
      --companion <file> --converter <file> --notices <file> --library-version <v> \\
      --library-commit <sha> --legacy-archive <zip> --legacy-manifest <json> --out <dir>
  migration-bridge.py plan --version <v> --releases <json>

`--companion` and `--converter` are programs stamped with `--version`:

  CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=<v>" -o <out> ./cmd/companion

and the same for ./cmd/mapio. build/release.sh ties a version to the commit count, and
a bridge needs a version no release used, so it is built by hand for that.
"""

import argparse
import json
import os
import re
import shutil
import stat
import sys
import tempfile
import zipfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import releaselib  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
PINS = os.path.join(HERE, "migration-bridge.json")
PINS_SCHEMA = "aucom.migration-bridge-pins/1"
BRIDGE_KIND = "aucom.migration-bridge/1"
BUNDLE_SCHEMA = "aucom.bundle-manifest/1.2"
RELEASE_SCHEMA = "aucom.release-manifest/1.0"
BRIDGE_NOTICES = "NOTICES-migration-bridge.txt"
EXTRACTOR = "auto-pigeon-extractor"
CONVERTER_SPDX = "Apache-2.0 AND MIT"
FULL_SHA = re.compile(r"^[0-9a-f]{40}$")
VERSION = re.compile(r"^[0-9]{1,4}\.[0-9]{1,7}$")

# What the older evaluator does, as read from internal/managedtools at the pinned
# commit: it reads at most this many manifests, newest version first, before it
# gives up looking for one it can take.
OLD_MANIFESTS_READ = 6


class Refusal(Exception):
    pass


def load_json(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def write_json(path, document):
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(document, handle, indent=2, sort_keys=False)
        handle.write("\n")


def version_key(version):
    major, _, minor = version.partition(".")
    return int(major), int(minor)


def check_version(version, pins):
    if not VERSION.match(version):
        raise Refusal(f"{version!r} is not a Companion release version")
    if version_key(version) <= version_key(pins["legacy"]["version"]):
        raise Refusal(f"{version} is not newer than {pins['legacy']['version']}, so no older Companion would be offered it")


def read_legacy_bundle(archive, platform, pin, work):
    """The historical extractor and its manifest entry, from the pinned archive."""
    with zipfile.ZipFile(archive) as source:
        top = f"auto-pigeon-companion-{pin['version']}-{platform}/"
        manifest = json.loads(source.read(top + f"{releaselib.DEPENDENCIES}/bundle-manifest.json"))
        extractor = manifest["extractor"]
        name = top + extractor["file"]
        target = os.path.join(work, EXTRACTOR + releaselib.exe_suffix(platform))
        with source.open(name) as packed, open(target, "wb") as out:
            shutil.copyfileobj(packed, out)
    os.chmod(target, 0o755)
    if releaselib.platform_of(target) != platform:
        raise Refusal(f"the historical extractor in {os.path.basename(archive)} is not built for {platform}")
    listed = next((m for m in manifest["members"] if m["path"] == extractor["file"]), None)
    if listed is None or listed["product"] != EXTRACTOR or listed["version"] != extractor["version"]:
        raise Refusal(f"{os.path.basename(archive)} does not list its extractor as {EXTRACTOR}")
    size, digest = releaselib.digest_of(target)
    if size != listed["size"] or digest != listed["sha256"]:
        raise Refusal(f"the extractor in {os.path.basename(archive)} is not the one its bundle manifest lists")
    return target, extractor, manifest


def check_legacy_inputs(args, pins):
    pin = pins["legacy"]
    if args.platform not in pin["targets"]:
        raise Refusal(f"build/migration-bridge.json pins no archive for {args.platform}")
    expected = pin["targets"][args.platform]
    if releaselib.digest_of(args.legacy_manifest)[1] != pin["release_manifest_sha256"]:
        raise Refusal(f"{args.legacy_manifest} is not the release manifest of {pin['tag']}")
    document = load_json(args.legacy_manifest)
    if document.get("schema") != RELEASE_SCHEMA or document.get("tag") != pin["tag"] or document.get("commit") != pin["commit"]:
        raise Refusal(f"{args.legacy_manifest} does not describe {pin['tag']} at {pin['commit']}")
    if os.path.basename(args.legacy_archive) != expected["archive"]:
        raise Refusal(f"{args.legacy_archive} is not {expected['archive']}")
    if releaselib.digest_of(args.legacy_archive)[1] != expected["sha256"]:
        raise Refusal(f"{args.legacy_archive} is not the archive {pin['tag']} published")
    return document


def stage_bundle(args, root, extractor_file, extractor):
    """Lay out the bundle and return its members, each named for what it is."""
    suffix = releaselib.exe_suffix(args.platform)
    deps = os.path.join(root, releaselib.DEPENDENCIES)
    os.makedirs(deps)
    companion = os.path.join(root, releaselib.COMPANION + suffix)
    converter = os.path.join(deps, releaselib.CONVERTER + suffix)
    shutil.copyfile(args.companion, companion)
    shutil.copyfile(args.converter, converter)
    shutil.copyfile(extractor_file, os.path.join(deps, os.path.basename(extractor_file)))
    shutil.copyfile(args.notices, os.path.join(deps, releaselib.NOTICES))
    for path in (companion, converter, os.path.join(deps, os.path.basename(extractor_file))):
        os.chmod(path, os.stat(path).st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    with open(os.path.join(deps, BRIDGE_NOTICES), "w", encoding="utf-8") as handle:
        handle.write(bridge_notices(args, extractor))
    for path, name in ((companion, "Companion"), (converter, "converter")):
        if releaselib.platform_of(path) != args.platform:
            raise Refusal(f"the {name} is not built for {args.platform}")

    _, digest = releaselib.digest_of(companion)
    with open(os.path.join(root, "SHA256SUMS"), "w", encoding="utf-8") as handle:
        handle.write(f"{digest[7:]}  {releaselib.COMPANION}{suffix}\n")

    product = {
        releaselib.COMPANION + suffix: ("auto-pigeon-companion", args.version),
        "SHA256SUMS": ("auto-pigeon-companion", args.version),
        f"{releaselib.DEPENDENCIES}/{releaselib.CONVERTER}{suffix}": (releaselib.CONVERTER, args.version),
        f"{releaselib.DEPENDENCIES}/{releaselib.NOTICES}": (releaselib.CONVERTER, args.version),
        f"{releaselib.DEPENDENCIES}/{BRIDGE_NOTICES}": ("auto-pigeon-companion", args.version),
        extractor["file"]: (EXTRACTOR, extractor["version"]),
    }
    members = []
    for relative in sorted(product):
        size, sha = releaselib.digest_of(os.path.join(root, relative))
        members.append({"path": relative, "product": product[relative][0], "version": product[relative][1],
                        "platform": args.platform, "size": size, "sha256": sha})
    return members


def bridge_notices(args, extractor):
    return "\n".join([
        f"Auto-Pigeon Companion {args.version}: a migration bridge",
        "",
        "This folder holds three programs, under three licences.",
        "",
        "  companion                  the MIT License",
        f"  {releaselib.CONVERTER:<26} {CONVERTER_SPDX}: see {releaselib.NOTICES}",
        f"  {EXTRACTOR:<26} {extractor['license']}, version {extractor['version']}",
        "",
        f"The Companion runs {releaselib.CONVERTER}. It does not run {EXTRACTOR}.",
        "",
        f"{EXTRACTOR} is here only because the updater in the Companion you had before this one",
        "will not take a bundle without one. It is the authentic file from the release that",
        "updater shipped in, byte for byte, and its licence is not changed by being here. The",
        "next ordinary update of this Companion removes it.",
        "",
    ])


def bundle_manifest(args, members, extractor):
    suffix = releaselib.exe_suffix(args.platform)
    return {
        "schema_version": BUNDLE_SCHEMA,
        "product": "auto-pigeon-companion",
        "version": args.version,
        "platform": args.platform,
        "companion": releaselib.COMPANION + suffix,
        "members": members,
        "licenses": [
            {"product": "auto-pigeon-companion", "spdx": "MIT", "covers": "the Companion's own code only"},
            {"product": releaselib.CONVERTER, "spdx": CONVERTER_SPDX,
             "covers": "the map converter this Companion runs: go/mapio and its wrapper"},
            {"product": EXTRACTOR, "spdx": extractor["license"],
             "covers": "the historical extractor executable, kept only for an older updater and never run; "
                       "not covered by the Companion's MIT licence"},
        ],
        "extractor": extractor,
        "converter": {
            "product": releaselib.CONVERTER, "version": args.version, "platform": args.platform,
            "file": f"{releaselib.DEPENDENCIES}/{releaselib.CONVERTER}{suffix}", "license": CONVERTER_SPDX,
            "source_commit": args.commit,
            "notices": f"{releaselib.DEPENDENCIES}/{releaselib.NOTICES}",
            "library": {"version": args.library_version, "commit": args.library_commit},
        },
        "migration_bridge": bridge_record(args, extractor),
    }


def bridge_record(args, extractor):
    return {
        "kind": BRIDGE_KIND,
        "purpose": "Carries this Companion to a Companion that reads only the older manifests. Not an ordinary "
                   "release, and the next ordinary update removes the extractor.",
        "runs_extractor": False,
        "extractor_sha256": next(m for m in args.members if m["path"] == extractor["file"])["sha256"],
        "notices": f"{releaselib.DEPENDENCIES}/{BRIDGE_NOTICES}",
    }


def release_manifest(args, legacy, archive):
    size, digest = releaselib.digest_of(archive)
    return {
        "schema": RELEASE_SCHEMA,
        "product": "auto-pigeon-companion",
        "version": args.version,
        "tag": f"v{args.version}",
        "commit": args.commit,
        "prerelease": True,
        "signed": False,
        "aue": legacy["aue"],
        "targets": [{
            "target": args.platform.replace("-", "/", 1),
            "verdict": "bundled_release",
            "reason": "a migration bridge for an older Companion's updater; see migration_bridge",
            "aucom_state": "manual_pending",
            "aue_status": "historical",
            "native_acceptance_in_ci": False,
            "archive": os.path.basename(archive),
            "size": size,
            "sha256": digest,
        }],
        "migration_bridge": {
            "kind": BRIDGE_KIND,
            "from_release": legacy["tag"],
            "from_commit": legacy["commit"],
            "extractor_is_historical": True,
            "runs_extractor": False,
        },
    }


def verify_bundle(args, root, members):
    """Read the finished folder the way the older reader will, and refuse early."""
    present = set()
    for directory, _, names in os.walk(root):
        for name in names:
            present.add(os.path.relpath(os.path.join(directory, name), root).replace(os.sep, "/"))
    listed = {member["path"]: member for member in members}
    if present - {f"{releaselib.DEPENDENCIES}/bundle-manifest.json"} != set(listed):
        raise Refusal(f"the bridge holds {sorted(present)} and lists {sorted(listed)}")
    for path, member in listed.items():
        size, digest = releaselib.digest_of(os.path.join(root, path))
        if size != member["size"] or digest != member["sha256"]:
            raise Refusal(f"{path} does not match its manifest entry")


def cmd_build(args):
    pins = load_json(args.pins)
    if pins.get("schema") != PINS_SCHEMA:
        raise Refusal(f"{args.pins} is not {PINS_SCHEMA}")
    check_version(args.version, pins)
    if args.platform.startswith("darwin-"):
        raise Refusal("a macOS application is replaced by hand, so no updater takes a bridge there")
    if not FULL_SHA.match(args.commit) or not FULL_SHA.match(args.library_commit):
        raise Refusal("--commit and --library-commit are full commit SHAs")
    with open(args.notices, encoding="utf-8") as handle:
        if args.library_version not in handle.read():
            raise Refusal(f"{args.notices} does not name {args.library_version}")
    legacy = check_legacy_inputs(args, pins)
    os.makedirs(args.out, exist_ok=True)
    work = tempfile.mkdtemp(prefix="aucom-bridge-")
    try:
        extractor_file, extractor, _ = read_legacy_bundle(args.legacy_archive, args.platform, pins["legacy"], work)
        expected = pins["legacy"]["targets"][args.platform]["extractor_sha256"]
        if releaselib.digest_of(extractor_file)[1] != expected:
            raise Refusal(f"the extractor is not the one pinned for {args.platform}")
        top = f"auto-pigeon-companion-{args.version}-{args.platform}"
        root = os.path.join(work, "bundle", top)
        os.makedirs(root)
        args.members = stage_bundle(args, root, extractor_file, extractor)
        write_json(os.path.join(root, releaselib.DEPENDENCIES, "bundle-manifest.json"),
                   bundle_manifest(args, args.members, extractor))
        verify_bundle(args, root, args.members)
        archive = os.path.join(args.out, top + ".zip")
        releaselib.write_zip(root, archive)
        document = release_manifest(args, legacy, archive)
        write_json(os.path.join(args.out, "release-manifest.json"), document)
        names = sorted(name for name in os.listdir(args.out) if name != "SHA256SUMS")
        with open(os.path.join(args.out, "SHA256SUMS"), "w", encoding="utf-8") as handle:
            for name in names:
                handle.write(f"{releaselib.digest_of(os.path.join(args.out, name))[1][7:]}  {name}\n")
    finally:
        shutil.rmtree(work, ignore_errors=True)
    print(archive)


def plan(version, releases, pins):
    """Whether an older Companion would reach this version, as its evaluator walks the list."""
    check_version(version, pins)
    tags = [release["tag_name"] for release in releases]
    if f"v{version}" in tags:
        raise Refusal(f"v{version} is already published, and a tag or asset is never replaced")
    # The list comes from one page, newest by date, and a bridge published now is first on it whatever
    # its number. Past that, only the number matters.
    newer = sorted((tag for tag in tags if version_key(tag.lstrip("v")) > version_key(version)),
                   key=lambda tag: version_key(tag.lstrip("v")), reverse=True)
    if len(newer) >= OLD_MANIFESTS_READ:
        raise Refusal(f"{len(newer)} releases are newer than {version} and an older Companion reads "
                      f"{OLD_MANIFESTS_READ} manifests, so it would never reach this one: {newer[:OLD_MANIFESTS_READ]}")
    return newer


def cmd_plan(args):
    newer = plan(args.version, load_json(args.releases), load_json(args.pins))
    print(f"v{args.version} is reachable: an older Companion reads {len(newer)} newer manifest(s) first "
          f"({', '.join(newer) or 'none'}), and takes it on read {len(newer) + 1} of {OLD_MANIFESTS_READ}.")
    print(f"It stays reachable for {OLD_MANIFESTS_READ - 1 - len(newer)} more ordinary release(s).")


def main(argv):
    parser = argparse.ArgumentParser(description="Prepare a migration bridge. It publishes nothing.")
    sub = parser.add_subparsers(dest="command", required=True)
    p = sub.add_parser("build")
    for name in ("version", "commit", "platform", "companion", "converter", "notices", "library-version",
                 "library-commit", "legacy-archive", "legacy-manifest", "out"):
        p.add_argument("--" + name, required=True)
    p.add_argument("--pins", default=PINS)
    p.set_defaults(run=cmd_build)
    p = sub.add_parser("plan")
    p.add_argument("--version", required=True)
    p.add_argument("--releases", required=True, help="the release list, as `gh api` prints it")
    p.add_argument("--pins", default=PINS)
    p.set_defaults(run=cmd_plan)
    args = parser.parse_args(argv)
    try:
        args.run(args)
    except Refusal as refusal:
        print(f"error: {refusal}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
