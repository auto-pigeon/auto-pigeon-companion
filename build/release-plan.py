#!/usr/bin/env python3
"""The release's decisions, as one program the workflow calls (NEW_247A).

`.github/workflows/release.yml` builds and publishes; everything it DECIDES is
here, where a test can drive it: which go/mapio revision the converter is built
from, which targets are released, how a bundle is made and checked, what the
release says about itself, which files are attached, and whether a rerun may
touch a release that already exists. The YAML carries no target list, no bundle
format and no version arithmetic of its own.

usage:
  release-plan.py pin            [--build build/converter.json]
  release-plan.py matrix         --aucom-support <json> --out <matrix.json> [--build ...]
  release-plan.py acceptance-matrix --matrix <matrix.json>
  release-plan.py check-converter --matrix <m> --aucom-dist <dir> --version <v> --commit <sha> --go go
                                 --out <converter-inputs.json> [--build ...]
  release-plan.py bundle         --matrix <m> --converter-inputs <j> --aucom-dist <dir> --version <v> --out <dir>
  release-plan.py verify-archive <zip> --platform <p> --version <v> --converter-inputs <j>
                                 [--commit <sha> --go go]
  release-plan.py release        --matrix <m> --converter-inputs <j> --bundles <dir> --version <v> --commit <sha> --out <dir>
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

BUILD_SCHEMA = "aucom.converter-build/1.0"
MATRIX_SCHEMA = "aucom.release-matrix/2.0"
RELEASE_SCHEMA = "aucom.release-manifest/2.0"
FULL_SHA = re.compile(r"^[0-9a-f]{40}$")
VERSION = re.compile(r"^1\.[0-9]+$")
REPOSITORY = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")

BUNDLED = "bundled_release"
BUILD_ONLY = "build_only"
REFUSED = "refused"

# The native GitHub runners the acceptance job uses, one per operating-system
# family. This is not a target list — the targets come from native-support.json
# and build/converter.json — it is which hosted machine can EXECUTE a target
# natively. A target not here is never claimed as natively accepted by
# CI, and an x64 runner is never used for an arm64 target.
NATIVE_RUNNERS = {
    "linux-amd64": "ubuntu-latest",
    "windows-amd64": "windows-latest",
    "darwin-arm64": "macos-latest",
}

SOURCE_URL = "https://github.com/{repository}/tree/{commit}"
COMPANION_REPOSITORY = "auto-pigeon/auto-pigeon-companion"


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
# What the converter is built from


def load_build(path):
    """Read and check build/converter.json. Every rule is a refusal with a reason."""
    build = load_json(path)
    if build.get("schema") != BUILD_SCHEMA:
        raise Refusal(f"{path} is {build.get('schema')!r}; this release reads {BUILD_SCHEMA!r}")
    for stale in ("commit", "version", "ref"):
        if stale in build:
            raise Refusal(f"{path}: {stale!r} is not written here. go.mod is the one place that names the "
                          "go/mapio revision, so one commit of this repository always names one converter")
    if not REPOSITORY.match(build.get("library_repository", "")):
        raise Refusal(f"{path}: library_repository {build.get('library_repository')!r} is not <owner>/<name>")
    if not re.match(r"^[0-9]+\.[0-9]+$", build.get("required_protocol", "")):
        raise Refusal(f"{path}: required_protocol {build.get('required_protocol')!r} is not <major>.<minor>")
    spdx = build.get("license", "")
    if "APACHE-2.0" not in spdx.upper() or spdx.upper().startswith("MIT"):
        raise Refusal(f"{path}: license {spdx!r}; the converter links an Apache-2.0 library, and an archive "
                      "listing it as MIT alone would read as entirely MIT")
    return build


def pinned_version(module, go_mod=None):
    """The exact version go.mod requires, which is the one revision a release builds."""
    path = go_mod or os.path.join(REPO, "go.mod")
    with open(path, encoding="utf-8") as handle:
        text = handle.read()
    found = re.findall(r"^\s*(?:require\s+)?" + re.escape(module) + r"\s+(\S+)\s*$", text, re.M)
    if len(found) != 1:
        raise Refusal(f"{path} requires {module} {len(found)} time(s); a release builds exactly one revision")
    if re.search(r"^\s*replace\b.*" + re.escape(module), text, re.M):
        raise Refusal(f"{path} replaces {module}: a clean checkout could not build what that names")
    return found[0]


def protocol_satisfies(speaks, minimum):
    """The same rule as internal/converter.ProtocolSatisfies: equal major, minor at least."""
    try:
        s_major, s_minor = (int(part) for part in speaks.split("."))
        w_major, w_minor = (int(part) for part in minimum.split("."))
    except ValueError:
        return False
    return s_major == w_major and s_minor >= w_minor


def cmd_pin(args):
    build = load_build(args.build)
    print(f"library_module={build['library_module']}")
    print(f"library_version={pinned_version(build['library_module'], args.go_mod)}")
    print(f"library_repository={build['library_repository']}")
    print(f"required_protocol={build['required_protocol']}")


# --------------------------------------------------------------------------
# The join


def join(aucom_rows, build):
    """One verdict per AUCOM target, and no target dropped without a word.

    The converter is pure Go and is built for every target the Companion is. What
    it has never been run on is its own statement, in build/converter.json; it
    inherits nothing from another program's support matrix."""
    build_only = build.get("build_only", {})
    known = {row["target"] for row in aucom_rows}
    unknown = sorted(set(build_only) - known)
    if unknown:
        raise Refusal(f"build/converter.json names {unknown}, which auto-pigeon-companion does not declare")
    targets = []
    for row in sorted(aucom_rows, key=lambda r: r["target"]):
        target = row["target"]
        entry = {
            "target": target,
            "platform": dash(target),
            "aucom": {"built": row["built"], "state": row["state"]},
        }
        if not row["built"]:
            entry["converter"] = {"status": REFUSED}
            entry["verdict"], entry["reason"] = REFUSED, (
                f"auto-pigeon-companion declares no artifact for {target} (internal/release/native-support.json)")
        elif target in build_only:
            entry["converter"] = {"status": BUILD_ONLY, "reason": build_only[target]}
            entry["verdict"], entry["reason"] = BUILD_ONLY, (
                "both programs compile for it, and auto-pigeon-mapio has never been executed on it: "
                + build_only[target])
        else:
            entry["converter"] = {"status": "published"}
            entry["verdict"], entry["reason"] = BUNDLED, "both programs are built for it"
        targets.append(entry)
    return {"schema": MATRIX_SCHEMA, "targets": targets}


def cmd_matrix(args):
    support = load_json(args.aucom_support)
    matrix = join(support["platforms"], load_build(args.build))
    write_json(args.out, matrix)
    for entry in matrix["targets"]:
        print(f"{entry['target']:15} {entry['verdict']:16} aucom={entry['aucom']['state']:15} "
              f"converter={entry['converter']['status']}")


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
# The converter this release built, checked before any of it enters a bundle


def go_modules(go, path):
    """`go version -m`: what the toolchain recorded a binary as linking, by module."""
    output = subprocess.run([go, "version", "-m", path], check=True, capture_output=True, text=True).stdout
    modules = {}
    for line in output.splitlines():
        parts = line.strip().split("\t")
        if len(parts) >= 3 and parts[0] == "dep":
            modules[parts[1]] = parts[2]
    return modules


def library_commit(go, module, version, go_mod_dir=None):
    """The full commit of a pinned pseudo-version, as Go's own resolution reports it."""
    done = subprocess.run([go, "mod", "download", "-json", f"{module}@{version}"], check=True, capture_output=True,
                          text=True, cwd=go_mod_dir or REPO)
    commit = json.loads(done.stdout).get("Origin", {}).get("Hash", "")
    if not FULL_SHA.match(commit):
        raise Refusal(f"Go reports no commit for {module}@{version} (got {commit!r})")
    if not version.endswith(commit[:12]):
        raise Refusal(f"{module}@{version} resolved to {commit}, which is not the commit that version names")
    return commit


def converter_protocol(go, entries, build):
    """Ask the one built converter this machine can run what it is."""
    host = releaselib.host_platform()
    target = entries.get(host)
    if target is None:
        raise Refusal(f"this release built no converter for {host}, so none could be asked what it speaks")
    done = subprocess.run([target["path"], "protocol", "--json"], check=True, capture_output=True, text=True)
    document = json.loads(done.stdout)
    if document.get("program") != build["program"]:
        raise Refusal(f"the built converter calls itself {document.get('program')!r}, not {build['program']}")
    if not protocol_satisfies(document.get("protocol", ""), build["required_protocol"]):
        raise Refusal(f"the built converter speaks protocol {document.get('protocol')!r} and this Companion "
                      f"requires {build['required_protocol']}")
    if document.get("license", {}).get("spdx") != build["license"]:
        raise Refusal(f"the built converter says its licence is {document.get('license', {}).get('spdx')!r}; "
                      f"build/converter.json says {build['license']!r}")
    return document


def check_converter(matrix, dist, build, version, commit, go, go_mod=None, resolve=library_commit):
    if not FULL_SHA.match(commit):
        raise Refusal(f"the commit {commit!r} is not a full 40-character lowercase commit SHA")
    module = build["library_module"]
    pinned = pinned_version(module, go_mod)
    notices = os.path.join(dist, "converter", releaselib.NOTICES)
    if not os.path.isfile(notices):
        raise Refusal(f"{notices} is not there; build/release.sh writes it, and the converter's licences require it")
    with open(notices, encoding="utf-8") as handle:
        text = handle.read()
    for required in ("MIT License", "Apache License", pinned):
        if required not in text:
            raise Refusal(f"{notices} does not carry {required!r}")

    inputs, toolchain = {}, ""
    for entry in matrix["targets"]:
        if entry["verdict"] == REFUSED:
            continue
        platform = entry["platform"]
        name = build["program"] + releaselib.exe_suffix(platform)
        path = os.path.join(dist, "converter", platform, name)
        if not os.path.isfile(path):
            raise Refusal(f"build/release.sh produced no converter for {entry['target']} at {path}")
        built_for = releaselib.platform_of(path)
        if built_for != platform:
            raise Refusal(f"{path} is built for {built_for}, and it is filed as {entry['target']}")
        settings = go_build_settings(go, path)
        if settings.get("CGO_ENABLED") != "0":
            raise Refusal(f"{path} was built with CGO_ENABLED={settings.get('CGO_ENABLED')!r}")
        linked = go_modules(go, path)
        if linked != {module: pinned}:
            raise Refusal(f"{path} links {linked}; a converter links {module} {pinned} and nothing else")
        size, digest = releaselib.digest_of(path)
        status = "published" if entry["verdict"] == BUNDLED else BUILD_ONLY
        inputs[platform] = {"path": os.path.abspath(path), "sha256": digest, "size": size, "status": status}
    if not inputs:
        raise Refusal("no target has a converter")
    protocol = converter_protocol(go, inputs, build)
    toolchain = subprocess.run([go, "env", "GOVERSION"], check=True, capture_output=True, text=True).stdout.strip()
    return {
        "program": build["program"],
        "version": version,
        "commit": commit,
        "repository": COMPANION_REPOSITORY,
        "protocol": protocol["protocol"],
        "license": build["license"],
        "source": SOURCE_URL.format(repository=COMPANION_REPOSITORY, commit=commit),
        "go": toolchain,
        "notices": os.path.abspath(notices),
        "library": {
            "module": module,
            "version": pinned,
            "commit": resolve(go, module, pinned),
            "repository": build["library_repository"],
            "license": "Apache-2.0",
        },
        "targets": inputs,
    }


def cmd_check_converter(args):
    inputs = check_converter(load_json(args.matrix), args.aucom_dist, load_build(args.build), args.version,
                             args.commit, args.go)
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


def bundle_one(entry, converter, dist, version, out, work):
    platform = entry["platform"]
    extracted = os.path.join(work, "aucom", platform)
    os.makedirs(extracted)
    unpack_aucom(dist, version, platform, extracted)
    bundles = os.path.join(work, "bundles")
    target = converter["targets"][platform]
    command = [
        bash(), os.path.join(HERE, "bundle-sidecar.sh"),
        "--platform", platform, "--version", version,
        "--binary-dir", extracted, "--out", bundles,
        "--converter", target["path"],
        "--converter-version", converter["version"],
        "--converter-commit", converter["commit"],
        "--converter-spdx", converter["license"],
        "--converter-source", converter["source"],
        "--converter-notices", converter["notices"],
        "--library-version", converter["library"]["version"],
        "--library-commit", converter["library"]["commit"],
    ]
    subprocess.run(command, check=True)
    build_only = entry["verdict"] == BUILD_ONLY
    directory = os.path.join(out, "candidates") if build_only else out
    os.makedirs(directory, exist_ok=True)
    archive = os.path.join(directory, archive_name(version, platform, build_only))
    releaselib.write_zip(os.path.join(bundles, f"auto-pigeon-companion-{version}-{platform}"), archive)
    return archive


def cmd_bundle(args):
    matrix, converter = load_json(args.matrix), load_json(args.converter_inputs)
    os.makedirs(args.out, exist_ok=True)
    work = tempfile.mkdtemp(prefix="aucom-bundle-")
    try:
        for entry in matrix["targets"]:
            if entry["verdict"] == REFUSED:
                print(f"{entry['platform']:15} refused: {entry['reason']}")
                continue
            archive = bundle_one(entry, converter, args.aucom_dist, args.version, args.out, work)
            print(f"{entry['platform']:15} {entry['verdict']:16} {os.path.relpath(archive, args.out)}")
    finally:
        shutil.rmtree(work, ignore_errors=True)


# --------------------------------------------------------------------------
# One archive, checked as a user would receive it


def go_build_settings(go, path):
    """`go version -m`: what the toolchain recorded about a build.

    GOOS, GOARCH, CGO_ENABLED, `vcs.revision` and `vcs.modified`. NOT the -ldflags:
    with `-trimpath`, which both programs are built with, Go leaves them out, so
    the stamped version string cannot be read back from a cross-built binary. The
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


def verify_archive(archive, platform, version, converter, go=None, commit=None):
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

        carried = manifest.get("converter")
        if not carried or carried.get("file") != where["converter"] or where["converter"] not in present:
            raise Refusal(f"{archive} carries no auto-pigeon-mapio at {where['converter']}; a released "
                          "bundle is never the Companion alone")
        if where["companion"] not in present:
            raise Refusal(f"{archive} carries no Companion at {where['companion']}")
        strays = sorted(name for name in present if "auto-pigeon-extractor" in name)
        if strays:
            raise Refusal(f"{archive} carries {strays}; a release ships no Auto-Pigeon Extractor")
        if carried.get("version") != converter["version"] or listed[where["converter"]]["version"] != converter["version"]:
            raise Refusal(f"{archive}: the converter is listed as {carried.get('version')}; this release built "
                          f"{converter['version']}")
        if listed[where["converter"]]["product"] != releaselib.CONVERTER:
            raise Refusal(f"{archive}: the converter is listed as a member of {listed[where['converter']]['product']}")
        if carried.get("source_commit") != converter["commit"]:
            raise Refusal(f"{archive}: the converter was bundled from {carried.get('source_commit')}, not {converter['commit']}")
        if carried.get("library") != {key: converter["library"][key] for key in ("module", "version", "commit", "license")}:
            raise Refusal(f"{archive}: the converter names the library {carried.get('library')}; this release "
                          f"built {converter['library']}")
        if carried.get("license") != converter["license"]:
            raise Refusal(f"{archive}: the converter is listed under {carried.get('license')!r}")
        if carried.get("notices") != where["notices"] or where["notices"] not in present:
            raise Refusal(f"{archive} carries no converter notices at {where['notices']}")
        with open(os.path.join(bundle, where["notices"]), encoding="utf-8") as handle:
            notices = handle.read()
        for required in ("MIT License", "Apache License", converter["library"]["version"]):
            if required not in notices:
                raise Refusal(f"{archive}: the converter's notices do not carry {required!r}")

        for role, revision in (("companion", commit), ("converter", converter["commit"])):
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
                linked = go_modules(go, path)
                expected = {} if role == "companion" else {converter["library"]["module"]: converter["library"]["version"]}
                if linked != expected:
                    raise Refusal(f"{archive}: the {role} links {linked}; it should link {expected}")
        return {"archive": os.path.basename(archive), "platform": platform, "build_only": build_only}
    finally:
        shutil.rmtree(work, ignore_errors=True)


def cmd_verify_archive(args):
    result = verify_archive(args.archive, args.platform, args.version, load_json(args.converter_inputs), args.go,
                            args.commit)
    print(f"{result['archive']}: verified ({'build-only candidate' if result['build_only'] else 'release'})")


# --------------------------------------------------------------------------
# The release: manifest, checksums, notes, aggregate, upload list


def release_notes(document):
    converter = document["converter"]
    library = converter["library"]
    lines = [
        f"Auto-Pigeon Companion {document['version']} — development prerelease, built from "
        f"`{document['commit']}`.",
        "",
        f"Each archive carries the **auto-pigeon-mapio {converter['version']}** map converter beside the Companion, "
        f"built from this commit and `{library['module']}` at `{library['version']}` (invocation protocol "
        f"{converter['protocol']}). It replaces the Auto-Pigeon Extractor: no extractor is in these archives.",
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
        "converter on that machine, so they are **not supported downloads**. `refused` targets have no archive, "
        "and the release manifest says why. The Companion's own native evidence per target is the third column; "
        "`build_only` and `manual_pending` there are the ABSENCE of evidence, not a pass.",
        "",
        "**Nothing here is signed.** There is no Apple Developer ID and no Authenticode certificate, so macOS "
        "shows a Gatekeeper warning and Windows shows SmartScreen on first run. `SHA256SUMS` makes a corrupted "
        "or truncated download detectable; it is not a signature, because anybody who can replace the archives "
        "can replace the checksum file.",
        "",
        *licence_notes(converter["license"]),
        "",
        "The Companion downloads no program. The converter reaches it only in this archive, and the Companion "
        "checks its digest against `bundle-manifest.json` before running it.",
        "",
        "**Updating from an older Companion:** a Companion that bundled the Auto-Pigeon Extractor does not "
        "recognise this release's manifest, and says so. Download this archive once by hand, into a new "
        "folder beside the old one; your settings and maps are not in that folder, and updates work in place "
        "from then on.",
    ]
    return "\n".join(lines) + "\n"


def licence_notes(converter_spdx):
    """What the release notes say about licences. No sentence here calls the archive MIT: it holds an
    MIT program, Apache-2.0 data compiled into that program, and the converter under both licences."""
    return [
        "**These archives are not under one licence.** Auto-Pigeon Companion's own code is MIT "
        "(`LICENSE` in the Companion's repository). The contract files from auto-pigeon-libraries compiled into "
        f"it are Apache-2.0. The auto-pigeon-mapio converter is `{converter_spdx}`: the go/mapio library and a small "
        "wrapper, shipped beside the Companion and run as its own process, with their texts and the library's "
        "NOTICE in `NOTICES-auto-pigeon-mapio.txt` beside it. Third-party programs the Companion runs are the "
        "user's own, under their own licences. `THIRD_PARTY_NOTICES.md` in the Companion's repository says which "
        "is which, and each archive's `bundle-manifest.json` names every file's licence.",
    ]


def cmd_release(args):
    matrix, converter = load_json(args.matrix), load_json(args.converter_inputs)
    os.makedirs(args.out, exist_ok=True)
    targets = []
    for entry in matrix["targets"]:
        row = {
            "target": entry["target"],
            "verdict": entry["verdict"],
            "reason": entry["reason"],
            "aucom_state": entry["aucom"]["state"],
            "converter_status": entry["converter"]["status"],
            "native_acceptance_in_ci": entry["platform"] in NATIVE_RUNNERS and entry["verdict"] == BUNDLED,
        }
        if entry["verdict"] != REFUSED:
            build_only = entry["verdict"] == BUILD_ONLY
            relative = archive_name(args.version, entry["platform"], build_only)
            source = os.path.join(args.bundles, "candidates" if build_only else "", relative)
            verify_archive(source, entry["platform"], args.version, converter, args.go, args.commit)
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
        "converter": {
            "program": converter["program"],
            "repository": converter["repository"],
            "commit": converter["commit"],
            "version": converter["version"],
            "protocol": converter["protocol"],
            "license": converter["license"],
            "source": converter["source"],
            "go": converter["go"],
            "library": converter["library"],
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
    default_build = os.path.join(HERE, "converter.json")

    p = sub.add_parser("pin")
    p.add_argument("--build", default=default_build)
    p.add_argument("--go-mod", default=None)
    p.set_defaults(run=cmd_pin)

    p = sub.add_parser("matrix")
    p.add_argument("--build", default=default_build)
    p.add_argument("--aucom-support", required=True, help="`companion release support --json`")
    p.add_argument("--out", required=True)
    p.set_defaults(run=cmd_matrix)

    p = sub.add_parser("acceptance-matrix")
    p.add_argument("--matrix", required=True)
    p.set_defaults(run=cmd_acceptance_matrix)

    p = sub.add_parser("check-converter")
    p.add_argument("--build", default=default_build)
    p.add_argument("--matrix", required=True)
    p.add_argument("--aucom-dist", required=True)
    p.add_argument("--version", required=True)
    p.add_argument("--commit", required=True, help="the commit this release is built from")
    p.add_argument("--go", default="go")
    p.add_argument("--out", required=True)
    p.set_defaults(run=cmd_check_converter)

    p = sub.add_parser("bundle")
    p.add_argument("--matrix", required=True)
    p.add_argument("--converter-inputs", required=True)
    p.add_argument("--aucom-dist", required=True)
    p.add_argument("--version", required=True)
    p.add_argument("--out", required=True)
    p.set_defaults(run=cmd_bundle)

    p = sub.add_parser("verify-archive")
    p.add_argument("archive")
    p.add_argument("--platform", required=True)
    p.add_argument("--version", required=True)
    p.add_argument("--converter-inputs", required=True)
    p.add_argument("--commit", default=None, help="the Companion commit this archive was built from")
    p.add_argument("--go", default=None)
    p.set_defaults(run=cmd_verify_archive)

    p = sub.add_parser("release")
    p.add_argument("--matrix", required=True)
    p.add_argument("--converter-inputs", required=True)
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
