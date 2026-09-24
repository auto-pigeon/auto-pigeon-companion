#!/usr/bin/env python3
"""The release's decisions, as one program the workflow calls (NEW_247A).

`.github/workflows/release.yml` builds and publishes; everything it DECIDES is
here, where a test can drive it: which Auto-Pigeon Extractor revision is
bundled, which targets are released, how a bundle is made and checked, what the
release says about itself, which files are attached, and whether a rerun may
touch a release that already exists. The YAML carries no target list, no bundle
format and no version arithmetic of its own.

usage:
  release-plan.py pin            [--pin build/aue-pin.json]
  release-plan.py aue-checkout   --aue-dir <dir> [--pin ...]
  release-plan.py matrix         --aucom-support <json> --aue-targets <dir> --out <matrix.json>
  release-plan.py acceptance-matrix --matrix <matrix.json>
  release-plan.py check-aue      --matrix <m> --aue-release <dir> --aue-version <v> [--pin ...] --out <aue-inputs.json>
  release-plan.py bundle         --matrix <m> --aue-inputs <j> --aucom-dist <dir> --version <v> --out <dir>
  release-plan.py verify-archive <zip> --platform <p> --version <v> --aue-version <v> --aue-commit <sha>
                                 [--commit <sha> --go go]
  release-plan.py release        --matrix <m> --aue-inputs <j> --bundles <dir> --version <v> --commit <sha> --out <dir>
  release-plan.py upload-list    --release-dir <dir>
  release-plan.py reconcile      --release-dir <dir> --existing <tsv>
  release-plan.py promote-check  --release-dir <dir> --existing <tsv> --commit <sha>

Every subcommand exits 0 on success and 1 with one `error:` sentence on
refusal; 2 is a usage error.
"""

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
sys.path.insert(0, HERE)
# No __pycache__ beside the scripts: an untracked file in the checkout makes the
# next `go build` stamp vcs.modified=true, which the archive check refuses.
sys.dont_write_bytecode = True
import releaselib  # noqa: E402


def bash():
    """The bash on PATH, found the way a shell or Go's exec.LookPath finds it.

    A bare "bash" handed to subprocess on Windows is resolved by CreateProcess,
    which searches the system directory first and so starts WSL's launcher
    (which needs an installed distribution) instead of the Git for Windows bash
    on PATH. On Linux and macOS this is the same bash either way.
    """
    found = shutil.which("bash")
    if not found:
        raise Refusal("bash is not on PATH; the bundle is composed by build/bundle-sidecar.sh")
    return found

PIN_SCHEMA = "aucom.aue-pin/1.0"
MATRIX_SCHEMA = "aucom.release-matrix/1.0"
RELEASE_SCHEMA = "aucom.release-manifest/1.0"
AUE_MANIFEST_SCHEMA = "aue-release-manifest/1.0"
FULL_SHA = re.compile(r"^[0-9a-f]{40}$")
VERSION = re.compile(r"^1\.[0-9]+$")
REPOSITORY = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")

BUNDLED = "bundled_release"
BUILD_ONLY = "build_only"
REFUSED = "refused"

# The native GitHub runners the acceptance job uses, one per operating-system
# family. This is not a target list — the targets come from native-support.json
# and AUE's `aue-release targets` — it is which hosted machine can EXECUTE a
# target natively. A target not here is never claimed as natively accepted by
# CI, and an x64 runner is never used for an arm64 target.
NATIVE_RUNNERS = {
    "linux-amd64": "ubuntu-latest",
    "windows-amd64": "windows-latest",
    "darwin-arm64": "macos-latest",
}

AUE_SOURCE_URL = "https://github.com/{repository}/tree/{commit}"


class Refusal(Exception):
    pass


def load_json(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def write_json(path, document):
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(document, handle, indent=2)
        handle.write("\n")


def dash(target):
    return target.replace("/", "-")


# --------------------------------------------------------------------------
# The pin


def load_pin(path):
    """Read and check build/aue-pin.json. Every rule is a refusal with a reason."""
    pin = load_json(path)
    if pin.get("schema") != PIN_SCHEMA:
        raise Refusal(f"{path} is {pin.get('schema')!r}; this release reads {PIN_SCHEMA!r}")
    commit = pin.get("commit", "")
    if not FULL_SHA.match(commit):
        raise Refusal(
            f"{path}: commit {commit!r} is not a full 40-character lowercase commit SHA. A branch, a tag, a "
            "short SHA or `latest` can move, and the extractor a release bundles must not"
        )
    if not REPOSITORY.match(pin.get("repository", "")):
        raise Refusal(f"{path}: repository {pin.get('repository')!r} is not <owner>/<name>")
    if not re.match(r"^[0-9]+\.[0-9]+$", pin.get("required_protocol", "")):
        raise Refusal(f"{path}: required_protocol {pin.get('required_protocol')!r} is not <major>.<minor>")
    if not REPOSITORY.match(pin.get("aulibs_repository", "")):
        raise Refusal(f"{path}: aulibs_repository {pin.get('aulibs_repository')!r} is not <owner>/<name>")
    if pin.get("version") and not VERSION.match(pin["version"]):
        raise Refusal(f"{path}: version {pin['version']!r} is not the 1.<commit-count> shape")
    return pin


def protocol_satisfies(speaks, minimum):
    """The same rule as internal/aue.ProtocolSatisfies: equal major, minor at least."""
    try:
        s_major, s_minor = (int(part) for part in speaks.split("."))
        w_major, w_minor = (int(part) for part in minimum.split("."))
    except ValueError:
        return False
    return s_major == w_major and s_minor >= w_minor


def cmd_pin(args):
    pin = load_pin(args.pin)
    print(f"repository={pin['repository']}")
    print(f"commit={pin['commit']}")
    print(f"required_protocol={pin['required_protocol']}")
    print(f"version={pin.get('version', '')}")
    print(f"aulibs_repository={pin['aulibs_repository']}")


def cmd_aue_checkout(args):
    """The checked-out AUE is the pinned commit, and the AULIBS revision its
    committed data was synced from is the one to build it against."""
    pin = load_pin(args.pin)
    head = subprocess.run(["git", "-C", args.aue_dir, "rev-parse", "HEAD"], check=True,
                          capture_output=True, text=True).stdout.strip()
    if head != pin["commit"]:
        raise Refusal(f"the extractor checkout is at {head} and build/aue-pin.json pins {pin['commit']}")
    count = subprocess.run(["git", "-C", args.aue_dir, "rev-list", "--count", "HEAD"], check=True,
                           capture_output=True, text=True).stdout.strip()
    version = f"1.{count}"
    if count in ("", "1"):
        raise Refusal("the extractor checkout has no history; fetch-depth 0 is needed for its 1.<count> version")
    if pin.get("version") and pin["version"] != version:
        raise Refusal(f"build/aue-pin.json says version {pin['version']} and commit {pin['commit']} is {version}")
    aulibs = set()
    for relative in ("internal/apmap/embedded/schema/apmap-contract-provenance.json",
                     "internal/gameparams/embedded/games/game-params-provenance.json"):
        path = os.path.join(args.aue_dir, relative)
        if os.path.isfile(path):
            aulibs.add(load_json(path).get("aulibs_commit", ""))
    aulibs.discard("")
    if len(aulibs) != 1 or not FULL_SHA.match(next(iter(aulibs))):
        raise Refusal(f"the pinned extractor's embedded data names AULIBS commits {sorted(aulibs)}; want exactly one")
    print(f"version={version}")
    print(f"aulibs_commit={next(iter(aulibs))}")


# --------------------------------------------------------------------------
# The join


def parse_aue_targets(directory):
    """AUE's own answer, from the text `aue-release targets` prints."""
    def lines(name):
        with open(os.path.join(directory, name), encoding="utf-8") as handle:
            return [line.rstrip("\n") for line in handle if line.strip()]

    published = {}
    for line in lines("published.txt"):
        goos, goarch = line.split()[:2]
        published[f"{goos}/{goarch}"] = True
    candidates = {}
    for line in lines("candidates.txt"):
        parts = line.split(" ", 3)
        if len(parts) < 4 or parts[2] != BUILD_ONLY:
            raise Refusal(f"aue-release targets --candidates --reasons printed {line!r}")
        candidates[f"{parts[0]}/{parts[1]}"] = parts[3]
    unsupported = {}
    for line in lines("unsupported.txt"):
        parts = line.split(" ", 2)
        if len(parts) < 3:
            raise Refusal(f"aue-release targets --unsupported printed {line!r}")
        unsupported[f"{parts[0]}/{parts[1]}"] = parts[2]
    return published, candidates, unsupported


def join(aucom_rows, published, candidates, unsupported):
    """One verdict per AUCOM target, and no target dropped without a word."""
    targets = []
    for row in sorted(aucom_rows, key=lambda r: r["target"]):
        target = row["target"]
        entry = {
            "target": target,
            "platform": dash(target),
            "aucom": {"built": row["built"], "state": row["state"]},
        }
        if target in published:
            entry["aue"] = {"status": "published"}
        elif target in candidates:
            entry["aue"] = {"status": BUILD_ONLY, "reason": candidates[target]}
        elif target in unsupported:
            entry["aue"] = {"status": REFUSED, "reason": unsupported[target]}
        else:
            raise Refusal(
                f"the extractor's release authority says nothing about {target}: it is neither published, a "
                "candidate nor refused. A target is never dropped silently"
            )
        if not row["built"]:
            entry["verdict"], entry["reason"] = REFUSED, (
                f"auto-pigeon-companion declares no artifact for {target} (internal/release/native-support.json)")
        elif entry["aue"]["status"] == REFUSED:
            entry["verdict"], entry["reason"] = REFUSED, "auto-pigeon-extractor refuses it: " + entry["aue"]["reason"]
        elif entry["aue"]["status"] == BUILD_ONLY:
            entry["verdict"], entry["reason"] = BUILD_ONLY, (
                "both programs compile for it, and auto-pigeon-extractor has never been executed on it: "
                + entry["aue"]["reason"])
        else:
            entry["verdict"], entry["reason"] = BUNDLED, (
                "auto-pigeon-extractor publishes it and auto-pigeon-companion builds it")
        targets.append(entry)
    aucom_targets = {row["target"] for row in aucom_rows}
    return {
        "schema": MATRIX_SCHEMA,
        "targets": targets,
        "aue_only": sorted(set(published) - aucom_targets),
    }


def cmd_matrix(args):
    support = load_json(args.aucom_support)
    published, candidates, unsupported = parse_aue_targets(args.aue_targets)
    matrix = join(support["platforms"], published, candidates, unsupported)
    write_json(args.out, matrix)
    for entry in matrix["targets"]:
        print(f"{entry['target']:15} {entry['verdict']:16} aucom={entry['aucom']['state']:15} "
              f"aue={entry['aue']['status']}")


def acceptance_matrix(matrix):
    """The GitHub matrix for native acceptance: every released target a hosted
    runner can execute natively, and at least one per operating-system family
    that has a released target."""
    include, families, covered = [], set(), set()
    for entry in matrix["targets"]:
        if entry["verdict"] != BUNDLED:
            continue
        family = entry["platform"].split("-")[0]
        families.add(family)
        runner = NATIVE_RUNNERS.get(entry["platform"])
        if runner:
            include.append({"platform": entry["platform"], "runner": runner})
            covered.add(family)
    missing = sorted(families - covered)
    if missing:
        raise Refusal(f"no hosted runner can natively accept a released target of {missing}")
    return {"include": include}


def cmd_acceptance_matrix(args):
    print(json.dumps(acceptance_matrix(load_json(args.matrix)), separators=(",", ":")))


# --------------------------------------------------------------------------
# The extractor's release, checked before any of it enters a bundle


def check_aue(matrix, release_dir, pin, aue_version):
    manifest_path = os.path.join(release_dir, "release-manifest.json")
    manifest = load_json(manifest_path)
    if manifest.get("schema_version") != AUE_MANIFEST_SCHEMA:
        raise Refusal(f"the extractor's release manifest is {manifest.get('schema_version')!r}")
    if manifest.get("version") != aue_version:
        raise Refusal(f"the extractor's release manifest says {manifest.get('version')} and the pin is {aue_version}")
    source = manifest.get("source", {})
    if source.get("commit") != pin["commit"]:
        raise Refusal(f"the extractor was built from {source.get('commit')!r}; the pin is {pin['commit']}")
    # Whatever licence the pinned extractor declares travels with it, as its own
    # file, and is QUOTED from its release manifest everywhere downstream — the
    # bundle manifest, the release manifest, the release notes — so nothing here
    # can disagree with the build that is shipped. Today that is
    # LicenseRef-Auto-Pigeon-Proprietary (NEW_247G); an older pinned build says
    # AGPL-3.0-only, and its copies keep that licence. What is refused: no
    # licence at all, and MIT, which the extractor never was — an archive
    # listing it so would read as entirely MIT.
    spdx = manifest.get("license", {}).get("spdx", "")
    if not spdx:
        raise Refusal("the extractor's release manifest declares no licence")
    if spdx.strip().upper().startswith("MIT"):
        raise Refusal(f"the extractor's release manifest declares {spdx!r}; the extractor is not MIT, and an "
                      "archive listing it so would read as entirely MIT")
    if not protocol_satisfies(manifest.get("protocol", ""), pin["required_protocol"]):
        raise Refusal(f"the extractor speaks protocol {manifest.get('protocol')!r} and this Companion "
                      f"requires {pin['required_protocol']}")
    if manifest.get("toolchain", {}).get("cgo_enabled"):
        raise Refusal("the extractor was built with cgo enabled")
    if not os.path.isfile(os.path.join(release_dir, "LICENSE")):
        raise Refusal("the staged extractor release has no LICENSE")

    def find(entries, target):
        goos, goarch = target.split("/")
        for artifact in entries or []:
            if artifact.get("platform") == {"os": goos, "arch": goarch}:
                return artifact
        return None

    inputs = {}
    for entry in matrix["targets"]:
        if entry["verdict"] == REFUSED:
            continue
        if entry["verdict"] == BUNDLED:
            artifact, status = find(manifest.get("artifacts"), entry["target"]), "published"
        else:
            artifact, status = find(manifest.get("candidates"), entry["target"]), BUILD_ONLY
        if artifact is None or artifact.get("status") != status:
            raise Refusal(f"the extractor's release has no {status} artifact for {entry['target']}")
        if (status == BUILD_ONLY) != artifact["file"].startswith("candidates/") or ".." in artifact["file"]:
            raise Refusal(f"the extractor's {status} artifact for {entry['target']} is filed as {artifact['file']!r}")
        path = os.path.join(release_dir, artifact["file"])
        size, digest = releaselib.digest_of(path)
        if size != artifact["size"] or digest != artifact["sha256"]:
            raise Refusal(f"{path} is {size} bytes {digest}; the extractor's manifest says "
                          f"{artifact['size']} bytes {artifact['sha256']}")
        built_for = releaselib.platform_of(path)
        if built_for != entry["platform"]:
            raise Refusal(f"{path} is built for {built_for}, and the extractor's manifest files it as {entry['target']}")
        inputs[entry["platform"]] = {"path": os.path.abspath(path), "sha256": digest, "status": status}
    return {
        "version": aue_version,
        "commit": pin["commit"],
        "repository": pin["repository"],
        "protocol": manifest["protocol"],
        "license": spdx,
        "license_file": os.path.abspath(os.path.join(release_dir, "LICENSE")),
        "source": AUE_SOURCE_URL.format(repository=pin["repository"], commit=pin["commit"]),
        "go": manifest.get("toolchain", {}).get("go", ""),
        "targets": inputs,
    }


def cmd_check_aue(args):
    inputs = check_aue(load_json(args.matrix), args.aue_release, load_pin(args.pin), args.aue_version)
    write_json(args.out, inputs)
    for platform, entry in sorted(inputs["targets"].items()):
        print(f"{platform:15} {entry['status']:10} {entry['sha256']}")


# --------------------------------------------------------------------------
# Bundles


def archive_name(version, platform, build_only=False):
    suffix = "-build-only" if build_only else ""
    return f"auto-pigeon-companion-{version}-{platform}{suffix}.zip"


def unpack_aucom(dist, version, platform, dest):
    base = os.path.join(dist, f"auto-pigeon-companion-{version}-{platform}")
    if os.path.isfile(base + ".tar.gz"):
        with tarfile.open(base + ".tar.gz") as archive:
            archive.extractall(dest, filter="data")
    elif os.path.isfile(base + ".zip"):
        releaselib.extract_zip(base + ".zip", dest)
    else:
        raise Refusal(f"build/release.sh produced no archive for {platform} in {dist}")


def bundle_one(entry, aue, dist, version, out, work):
    platform = entry["platform"]
    extracted = os.path.join(work, "aucom", platform)
    os.makedirs(extracted)
    unpack_aucom(dist, version, platform, extracted)
    bundles = os.path.join(work, "bundles")
    target = aue["targets"][platform]
    command = [
        bash(), os.path.join(HERE, "bundle-sidecar.sh"),
        "--platform", platform, "--version", version,
        "--binary-dir", extracted, "--out", bundles,
        "--extractor", target["path"],
        "--extractor-version", aue["version"],
        "--extractor-commit", aue["commit"],
        "--extractor-license", aue["license_file"],
        "--extractor-spdx", aue["license"],
        "--extractor-source", aue["source"],
    ]
    subprocess.run(command, check=True)
    build_only = entry["verdict"] == BUILD_ONLY
    directory = os.path.join(out, "candidates") if build_only else out
    os.makedirs(directory, exist_ok=True)
    archive = os.path.join(directory, archive_name(version, platform, build_only))
    releaselib.write_zip(os.path.join(bundles, f"auto-pigeon-companion-{version}-{platform}"), archive)
    return archive


def cmd_bundle(args):
    matrix, aue = load_json(args.matrix), load_json(args.aue_inputs)
    os.makedirs(args.out, exist_ok=True)
    work = tempfile.mkdtemp(prefix="aucom-bundle-")
    try:
        for entry in matrix["targets"]:
            if entry["verdict"] == REFUSED:
                print(f"{entry['platform']:15} refused: {entry['reason']}")
                continue
            archive = bundle_one(entry, aue, args.aucom_dist, args.version, args.out, work)
            print(f"{entry['platform']:15} {entry['verdict']:16} {os.path.relpath(archive, args.out)}")
    finally:
        shutil.rmtree(work, ignore_errors=True)


# --------------------------------------------------------------------------
# One archive, checked as a user would receive it


def go_build_settings(go, path):
    """`go version -m`: what the toolchain recorded about a build.

    GOOS, GOARCH, `vcs.revision` and `vcs.modified`. NOT the -ldflags: with
    `-trimpath`, which both programs are built with, Go leaves them out, so the
    stamped version string cannot be read back from a cross-built binary. The
    commit can, and a version is `1.<commit-count>` of exactly one commit; the
    string itself is read by running `companion version` on the native
    runners (build/release-acceptance.py)."""
    output = subprocess.run([go, "version", "-m", path], check=True, capture_output=True, text=True).stdout
    settings = {}
    for line in output.splitlines():
        parts = line.strip().split("\t")
        if len(parts) >= 2 and parts[0] == "build":
            key, _, value = parts[1].partition("=")
            settings[key] = value
    return settings


def verify_archive(archive, platform, version, aue_version, aue_commit, go=None, commit=None):
    """Everything an eligible archive must be, or the first way it is not."""
    build_only = os.path.basename(archive) == archive_name(version, platform, True)
    if not build_only and os.path.basename(archive) != archive_name(version, platform):
        raise Refusal(f"{os.path.basename(archive)} is not named {archive_name(version, platform)}")
    work = tempfile.mkdtemp(prefix="aucom-verify-")
    try:
        releaselib.extract_zip(archive, work)
        tops = os.listdir(work)
        expected_top = f"auto-pigeon-companion-{version}-{platform}"
        if tops != [expected_top]:
            raise Refusal(f"{archive} unpacks to {tops}; want the one directory {expected_top}")
        bundle = os.path.join(work, expected_top)
        where = releaselib.layout(bundle, platform)
        manifest_path = os.path.join(bundle, where["manifest"])
        if not os.path.isfile(manifest_path):
            raise Refusal(f"{archive} has no {where['manifest']}")
        manifest = load_json(manifest_path)
        if manifest.get("version") != version or manifest.get("platform") != platform:
            raise Refusal(f"{archive}: the bundle manifest says {manifest.get('version')} {manifest.get('platform')}; "
                          f"the archive is {version} {platform}")
        listed = {member["path"]: member for member in manifest.get("members", [])}
        present = set()
        for root, _, names in os.walk(bundle):
            for name in names:
                path = os.path.join(root, name)
                relative = os.path.relpath(path, bundle).replace(os.sep, "/")
                if relative == where["manifest"]:
                    continue
                present.add(relative)
                member = listed.get(relative)
                if member is None:
                    raise Refusal(f"{archive}: {relative} is in the archive and not in its bundle manifest")
                size, digest = releaselib.digest_of(path)
                if size != member["size"] or digest != member["sha256"]:
                    raise Refusal(f"{archive}: {relative} does not match its bundle manifest entry")
                if member.get("platform") != platform:
                    raise Refusal(f"{archive}: {relative} is listed for {member.get('platform')}")
        missing = sorted(set(listed) - present)
        if missing:
            raise Refusal(f"{archive}: the bundle manifest lists {missing}, which the archive does not carry")

        extractor = manifest.get("extractor")
        if not extractor or extractor.get("file") != where["extractor"] or where["extractor"] not in present:
            raise Refusal(f"{archive} carries no auto-pigeon-extractor at {where['extractor']}; a released "
                          "bundle is never the Companion alone")
        if where["companion"] not in present:
            raise Refusal(f"{archive} carries no Companion at {where['companion']}")
        for required in ("LICENSE-auto-pigeon-companion.txt", "LICENSE-auto-pigeon-extractor.txt"):
            if required not in present:
                raise Refusal(f"{archive} has no {required}; each program travels with its own licence")
        if extractor.get("version") != aue_version or listed[where["extractor"]]["version"] != aue_version:
            raise Refusal(f"{archive}: the extractor is listed as {extractor.get('version')}; the pin is {aue_version}")
        if listed[where["extractor"]]["product"] != "auto-pigeon-extractor":
            raise Refusal(f"{archive}: the extractor is listed as a member of {listed[where['extractor']]['product']}")
        if extractor.get("source_commit") != aue_commit:
            raise Refusal(f"{archive}: the extractor was bundled from {extractor.get('source_commit')}, not {aue_commit}")

        for role, revision in (("companion", commit), ("extractor", aue_commit)):
            path = os.path.join(bundle, where[role])
            built_for = releaselib.platform_of(path)
            if built_for != platform:
                raise Refusal(f"{archive}: the {role} is built for {built_for}, in a {platform} archive")
            if go:
                settings = go_build_settings(go, path)
                if f"{settings.get('GOOS')}-{settings.get('GOARCH')}" != platform:
                    raise Refusal(f"{archive}: the Go toolchain recorded the {role} as "
                                  f"{settings.get('GOOS')}/{settings.get('GOARCH')}")
                if revision and settings.get("vcs.revision") != revision:
                    raise Refusal(f"{archive}: the {role} was built from {settings.get('vcs.revision')!r}, "
                                  f"not {revision}, so it is not the version this archive is named for")
                if settings.get("vcs.modified") != "false":
                    raise Refusal(f"{archive}: the {role} was built from a modified working tree "
                                  f"(vcs.modified={settings.get('vcs.modified')!r})")
        return {"archive": os.path.basename(archive), "platform": platform, "build_only": build_only}
    finally:
        shutil.rmtree(work, ignore_errors=True)


def cmd_verify_archive(args):
    result = verify_archive(args.archive, args.platform, args.version, args.aue_version, args.aue_commit, args.go,
                            args.commit)
    print(f"{result['archive']}: verified ({'build-only candidate' if result['build_only'] else 'release'})")


# --------------------------------------------------------------------------
# The release: manifest, checksums, notes, aggregate, upload list


def release_notes(document):
    aue = document["aue"]
    lines = [
        f"Auto-Pigeon Companion {document['version']} — development prerelease, built from "
        f"`{document['commit']}`.",
        "",
        f"Each archive carries **Auto-Pigeon Extractor {aue['version']}** beside the Companion, built from "
        f"`{aue['repository']}` at commit `{aue['commit']}` (invocation protocol {aue['protocol']}).",
        "",
        "| target | verdict | Companion native evidence | archive |",
        "| --- | --- | --- | --- |",
    ]
    for entry in document["targets"]:
        archive = entry.get("archive") or "—"
        lines.append(f"| {entry['target']} | {entry['verdict']} | {entry['aucom_state']} | {archive} |")
    lines += [
        "",
        "`bundled_release` archives are the downloads. `build_only` archives are under `candidates/` inside "
        f"`auto-pigeon-companion-{document['version']}-all.zip` only: they compile, and nobody has executed the "
        "extractor on that machine, so they are **not supported downloads**. `refused` targets have no archive, "
        "and the release manifest says why. The Companion's own native evidence per target is the third column; "
        "`build_only` and `manual_pending` there are the ABSENCE of evidence, not a pass.",
        "",
        "**Nothing here is signed.** There is no Apple Developer ID and no Authenticode certificate, so macOS "
        "shows a Gatekeeper warning and Windows shows SmartScreen on first run. `SHA256SUMS` makes a corrupted "
        "or truncated download detectable; it is not a signature, because anybody who can replace the archives "
        "can replace the checksum file.",
        "",
        *licence_notes(aue["license"]),
        "",
        "The Companion downloads no program. The extractor reaches it only in this archive, and the Companion "
        "checks its digest against `bundle-manifest.json` before running it.",
    ]
    return "\n".join(lines) + "\n"


PROPRIETARY = "LicenseRef-Auto-Pigeon-Proprietary"
COPYLEFT_PREFIXES = ("GPL-", "AGPL-", "LGPL-")


def licence_notes(extractor_spdx):
    """What the release notes say about licences. The extractor's identifier is
    quoted from its own release manifest, never restated, and no sentence here
    calls the archive MIT: it holds an MIT program, Apache-2.0 data compiled
    into that program, and the extractor under its own licence."""
    lines = [
        "**These archives are not under one licence.** Auto-Pigeon Companion's own code is MIT "
        "(`LICENSE-auto-pigeon-companion.txt`). The contract files from auto-pigeon-libraries compiled into it "
        "are Apache-2.0. Auto-Pigeon Extractor is a separate program under its own licence "
        f"(`LICENSE-auto-pigeon-extractor.txt`, declared `{extractor_spdx}`), shipped beside the Companion and run "
        "as its own process; the Companion's MIT licence does not cover it. Third-party programs the Companion "
        "runs are the user's own, under their own licences. `THIRD_PARTY_NOTICES.md` in every archive says which "
        "is which.",
    ]
    if extractor_spdx == PROPRIETARY:
        lines += [
            "",
            "**Auto-Pigeon Extractor is proprietary** (Copyright (c) 2026 Andrea D'Intino, all rights reserved). "
            "Its licence grants no right to use it except through a separate written authorization from the "
            "copyright owner; see `LICENSE-auto-pigeon-extractor.txt`.",
        ]
    elif extractor_spdx.startswith(COPYLEFT_PREFIXES):
        lines += [
            "",
            f"The extractor in these archives was built from a commit that declared `{extractor_spdx}`, and these "
            "copies are under that licence.",
        ]
    return lines


def cmd_release(args):
    matrix, aue = load_json(args.matrix), load_json(args.aue_inputs)
    os.makedirs(args.out, exist_ok=True)
    targets = []
    for entry in matrix["targets"]:
        row = {
            "target": entry["target"],
            "verdict": entry["verdict"],
            "reason": entry["reason"],
            "aucom_state": entry["aucom"]["state"],
            "aue_status": entry["aue"]["status"],
            "native_acceptance_in_ci": entry["platform"] in NATIVE_RUNNERS and entry["verdict"] == BUNDLED,
        }
        if entry["verdict"] != REFUSED:
            build_only = entry["verdict"] == BUILD_ONLY
            relative = archive_name(args.version, entry["platform"], build_only)
            source = os.path.join(args.bundles, "candidates" if build_only else "", relative)
            verify_archive(source, entry["platform"], args.version, aue["version"], aue["commit"], args.go,
                           args.commit)
            size, digest = releaselib.digest_of(source)
            if build_only:
                row["candidate"] = f"candidates/{relative}"
            else:
                row["archive"] = relative
                shutil.copyfile(source, os.path.join(args.out, relative))
            row["size"], row["sha256"] = size, digest
        targets.append(row)
    if not any(row["verdict"] == BUNDLED for row in targets):
        raise Refusal("no target is a bundled_release; there is nothing a user could download")

    document = {
        "schema": RELEASE_SCHEMA,
        "product": "auto-pigeon-companion",
        "version": args.version,
        "tag": f"v{args.version}",
        "commit": args.commit,
        "prerelease": True,
        "signed": False,
        "aue": {
            "repository": aue["repository"],
            "commit": aue["commit"],
            "version": aue["version"],
            "protocol": aue["protocol"],
            "license": aue["license"],
            "source": aue["source"],
            "go": aue["go"],
        },
        "targets": targets,
    }
    write_json(os.path.join(args.out, "release-manifest.json"), document)

    top = sorted(name for name in os.listdir(args.out) if name != "SHA256SUMS")
    with open(os.path.join(args.out, "SHA256SUMS"), "w", encoding="utf-8") as handle:
        for name in top:
            handle.write(f"{releaselib.digest_of(os.path.join(args.out, name))[1][7:]}  {name}\n")

    # The operator aggregate: the SAME archives, the candidates, the manifest
    # and the checksums, in one file. Built last, so it contains the others.
    staging = tempfile.mkdtemp(prefix="aucom-aggregate-")
    try:
        aggregate_root = os.path.join(staging, f"auto-pigeon-companion-{args.version}-all")
        os.makedirs(aggregate_root)
        for name in top + ["SHA256SUMS"]:
            shutil.copyfile(os.path.join(args.out, name), os.path.join(aggregate_root, name))
        candidates = os.path.join(args.bundles, "candidates")
        if os.path.isdir(candidates):
            shutil.copytree(candidates, os.path.join(aggregate_root, "candidates"))
        aggregate = os.path.join(args.out, f"auto-pigeon-companion-{args.version}-all.zip")
        releaselib.write_zip(aggregate_root, aggregate)
    finally:
        shutil.rmtree(staging, ignore_errors=True)
    with open(os.path.join(args.out, "SHA256SUMS"), "a", encoding="utf-8") as handle:
        handle.write(f"{releaselib.digest_of(aggregate)[1][7:]}  {os.path.basename(aggregate)}\n")

    if args.notes:
        with open(args.notes, "w", encoding="utf-8") as handle:
            handle.write(release_notes(document))
    for name in upload_list(args.out):
        print(name)


def upload_list(release_dir):
    """The exact files a release attaches, from its own manifest. Refuses a
    build-only candidate at the top level, a directory, a link, and any file it
    cannot account for."""
    document = load_json(os.path.join(release_dir, "release-manifest.json"))
    version = document["version"]
    expected = {"release-manifest.json", "SHA256SUMS", f"auto-pigeon-companion-{version}-all.zip"}
    for row in document["targets"]:
        if row.get("archive"):
            if row["verdict"] != BUNDLED:
                raise Refusal(f"{row['archive']} is {row['verdict']} and is listed as a release download")
            expected.add(row["archive"])
    names = sorted(os.listdir(release_dir))
    for name in names:
        path = os.path.join(release_dir, name)
        if os.path.islink(path) or not os.path.isfile(path):
            raise Refusal(f"{name} in the release directory is not a regular file")
        if "build-only" in name:
            raise Refusal(f"{name} is a build-only candidate; it is carried in the aggregate, never attached")
        if name not in expected:
            raise Refusal(f"{name} is in the release directory and the release manifest does not account for it")
    absent = sorted(expected - set(names))
    if absent:
        raise Refusal(f"the release directory is missing {absent}")
    return sorted(expected)


def cmd_upload_list(args):
    for name in upload_list(args.release_dir):
        print(name)


# --------------------------------------------------------------------------
# Reruns and promotion


def read_existing(path):
    """`<name>\\t<digest>` lines, from the GitHub API's asset digests."""
    existing = {}
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            if not line.strip():
                continue
            name, _, digest = line.rstrip("\n").partition("\t")
            existing[name] = digest.strip()
    return existing


def reconcile(release_dir, existing):
    """Which files to upload to a release that may already exist.

    A file already there with the SAME digest is left alone. A file already
    there with a DIFFERENT digest is a hard failure: the same commit built two
    different sets of bytes, or somebody changed the release, and either way
    nothing here overwrites it. An asset whose digest GitHub does not report
    cannot be compared and is a failure too, never an overwrite. So is an asset
    this build did not produce.
    """
    wanted = upload_list(release_dir)
    uploads, problems = [], []
    for name in wanted:
        local = releaselib.digest_of(os.path.join(release_dir, name))[1]
        if name not in existing:
            uploads.append(name)
        elif not existing[name] or existing[name] == "null":
            problems.append(f"{name} is already attached and GitHub reports no digest for it, so it cannot be "
                            "compared; it is not replaced")
        elif existing[name] != local:
            problems.append(f"{name} is already attached as {existing[name]} and this build made {local}")
    for name in sorted(set(existing) - set(wanted)):
        problems.append(f"{name} is attached to the release and this build did not produce it")
    if problems:
        raise Refusal("the release already exists and does not match this build — nothing was uploaded: "
                      + "; ".join(problems))
    return uploads


def cmd_reconcile(args):
    for name in reconcile(args.release_dir, read_existing(args.existing)):
        print(name)


def cmd_promote_check(args):
    """A prerelease may become a release when every asset on it is the one its
    manifest names, built from the commit being promoted."""
    document = load_json(os.path.join(args.release_dir, "release-manifest.json"))
    if document.get("commit") != args.commit:
        raise Refusal(f"the release was built from {document.get('commit')} and the promotion names {args.commit}")
    existing = read_existing(args.existing)
    listed = {row["archive"]: row["sha256"] for row in document["targets"] if row.get("archive")}
    for name, digest in listed.items():
        if existing.get(name) != digest:
            raise Refusal(f"{name} on the release is {existing.get(name)!r} and its manifest says {digest}")
    print(f"v{document['version']}: every archive on the release is the one its manifest names")


# --------------------------------------------------------------------------


def main(argv):
    parser = argparse.ArgumentParser(prog="release-plan.py")
    sub = parser.add_subparsers(dest="command", required=True)
    default_pin = os.path.join(HERE, "aue-pin.json")

    p = sub.add_parser("pin")
    p.add_argument("--pin", default=default_pin)
    p.set_defaults(run=cmd_pin)

    p = sub.add_parser("aue-checkout")
    p.add_argument("--pin", default=default_pin)
    p.add_argument("--aue-dir", required=True)
    p.set_defaults(run=cmd_aue_checkout)

    p = sub.add_parser("matrix")
    p.add_argument("--aucom-support", required=True, help="`companion release support --json`")
    p.add_argument("--aue-targets", required=True, help="published.txt, candidates.txt, unsupported.txt")
    p.add_argument("--out", required=True)
    p.set_defaults(run=cmd_matrix)

    p = sub.add_parser("acceptance-matrix")
    p.add_argument("--matrix", required=True)
    p.set_defaults(run=cmd_acceptance_matrix)

    p = sub.add_parser("check-aue")
    p.add_argument("--pin", default=default_pin)
    p.add_argument("--matrix", required=True)
    p.add_argument("--aue-release", required=True)
    p.add_argument("--aue-version", required=True)
    p.add_argument("--out", required=True)
    p.set_defaults(run=cmd_check_aue)

    p = sub.add_parser("bundle")
    p.add_argument("--matrix", required=True)
    p.add_argument("--aue-inputs", required=True)
    p.add_argument("--aucom-dist", required=True)
    p.add_argument("--version", required=True)
    p.add_argument("--out", required=True)
    p.set_defaults(run=cmd_bundle)

    p = sub.add_parser("verify-archive")
    p.add_argument("archive")
    p.add_argument("--platform", required=True)
    p.add_argument("--version", required=True)
    p.add_argument("--aue-version", required=True)
    p.add_argument("--aue-commit", required=True)
    p.add_argument("--commit", default=None, help="the Companion commit this archive was built from")
    p.add_argument("--go", default=None)
    p.set_defaults(run=cmd_verify_archive)

    p = sub.add_parser("release")
    p.add_argument("--matrix", required=True)
    p.add_argument("--aue-inputs", required=True)
    p.add_argument("--bundles", required=True)
    p.add_argument("--version", required=True)
    p.add_argument("--commit", required=True)
    p.add_argument("--out", required=True)
    p.add_argument("--notes", default=None)
    p.add_argument("--go", default=None)
    p.set_defaults(run=cmd_release)

    p = sub.add_parser("upload-list")
    p.add_argument("--release-dir", required=True)
    p.set_defaults(run=cmd_upload_list)

    p = sub.add_parser("reconcile")
    p.add_argument("--release-dir", required=True)
    p.add_argument("--existing", required=True)
    p.set_defaults(run=cmd_reconcile)

    p = sub.add_parser("promote-check")
    p.add_argument("--release-dir", required=True)
    p.add_argument("--existing", required=True)
    p.add_argument("--commit", required=True)
    p.set_defaults(run=cmd_promote_check)

    args = parser.parse_args(argv)
    try:
        args.run(args)
    except Refusal as err:
        print(f"error: {err}", file=sys.stderr)
        return 1
    except (ValueError, OSError, KeyError, subprocess.CalledProcessError) as err:
        print(f"error: {type(err).__name__}: {err}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
