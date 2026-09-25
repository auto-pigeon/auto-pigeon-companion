"""What the release tools share: digests, the platform a binary was built for,
and archives that are the same bytes every time they are made.

Imported by `build/bundle-manifest.py` and `build/release-plan.py`, which sit
beside it. Standard library only, like everything else the release runs.

# Why the platform is read out of the file

A bundle pairs two separately built programs. The one mistake nothing else
catches is pairing them for different machines: a linux/arm64 extractor beside a
linux/amd64 Companion is a bundle that unpacks, lists both digests correctly,
and fails on the user's machine with `exec format error`. The executable header
says which operating system and processor a file is for, so the bundle step
reads it rather than trusting a file name, and refuses a disagreement. A file
whose header it cannot read is refused too: "I could not tell" is not "it
matches".

# Why the archives are deterministic

Every push to main publishes, and a rerun of the same commit must be able to
show that it rebuilt the SAME bytes before it touches a release (NEW_247A). The
programs are reproducible (`CGO_ENABLED=0 -trimpath`); a zip is not, unless
every entry carries a fixed time, a fixed order and fixed permissions. That is
what `write_zip` does, so two builds of one commit produce one archive digest.
"""

import hashlib
import os
import stat
import struct
import zipfile

# 1980-01-01, the earliest time a zip entry can carry. Fixed, so the archive's
# bytes do not depend on when it was made.
ZIP_EPOCH = (1980, 1, 1, 0, 0, 0)

EXTRACTOR = "auto-pigeon-extractor"
# The folder beside the Companion that holds the extractor and the bundle
# manifest on Linux and Windows (internal/aue.DependenciesDir).
DEPENDENCIES = "dependencies"
COMPANION = "companion"
APP_NAME = "Auto-Pigeon Companion.app"


def digest_of(path):
    """(size, "sha256:<hex>") of a file."""
    sha = hashlib.sha256()
    size = 0
    with open(path, "rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            sha.update(block)
            size += len(block)
    return size, "sha256:" + sha.hexdigest()


def platform_of(path):
    """The `<goos>-<goarch>` an executable was built for, or raise ValueError.

    ELF is Linux, Mach-O is macOS and PE is Windows; the processor is the
    header's machine field. Only the six targets a release builds are named;
    anything else is an error that says what was found.
    """
    with open(path, "rb") as handle:
        head = handle.read(4096)
    if head[:4] == b"\x7fELF":
        if len(head) < 20 or head[5] != 1:
            raise ValueError(f"{path}: a truncated or big-endian ELF file")
        machine = struct.unpack_from("<H", head, 18)[0]
        arch = {0x3E: "amd64", 0xB7: "arm64"}.get(machine)
        if arch is None:
            raise ValueError(f"{path}: an ELF file for machine 0x{machine:x}, which no release builds")
        return "linux-" + arch
    if head[:4] == b"\xcf\xfa\xed\xfe":
        cputype = struct.unpack_from("<I", head, 4)[0]
        arch = {0x01000007: "amd64", 0x0100000C: "arm64"}.get(cputype)
        if arch is None:
            raise ValueError(f"{path}: a Mach-O file for CPU type 0x{cputype:x}, which no release builds")
        return "darwin-" + arch
    if head[:2] == b"MZ":
        if len(head) < 0x40:
            raise ValueError(f"{path}: a truncated PE file")
        offset = struct.unpack_from("<I", head, 0x3C)[0]
        if offset + 6 > len(head) or head[offset:offset + 4] != b"PE\x00\x00":
            raise ValueError(f"{path}: an MZ file with no PE header")
        machine = struct.unpack_from("<H", head, offset + 4)[0]
        arch = {0x8664: "amd64", 0xAA64: "arm64"}.get(machine)
        if arch is None:
            raise ValueError(f"{path}: a PE file for machine 0x{machine:x}, which no release builds")
        return "windows-" + arch
    raise ValueError(f"{path}: not an executable this release can identify (no ELF, Mach-O or PE header)")


def exe_suffix(platform):
    return ".exe" if platform.startswith("windows-") else ""


def app_dir(bundle):
    """The one `.app` directory directly inside a macOS bundle."""
    apps = sorted(name for name in os.listdir(bundle) if name.endswith(".app"))
    if len(apps) != 1:
        raise ValueError(f"{bundle}: a macOS bundle holds exactly one .app, and this one holds {apps}")
    return apps[0]


def layout(bundle, platform):
    """Where each program and the bundle manifest live in a bundle, as paths
    relative to its root.

    On Linux and Windows the Companion is at the root and the extractor and
    the manifest are in `dependencies/` (DEPENDENCIES; operator decision,
    2026-09-25), so the root holds only the program a person starts. On macOS,
    inside the .app: the
    Companion runs from `Contents/MacOS/` and looks for its extractor beside
    itself, and reads the manifest from `Contents/Resources/` (see
    internal/aue.bundleManifestFor, which is the other half of this).
    """
    suffix = exe_suffix(platform)
    if platform.startswith("darwin-"):
        app = app_dir(bundle)
        return {
            "companion": f"{app}/Contents/MacOS/{COMPANION}",
            "extractor": f"{app}/Contents/MacOS/{EXTRACTOR}",
            "manifest": f"{app}/Contents/Resources/bundle-manifest.json",
            "resources": f"{app}/Contents/Resources",
        }
    return {
        "companion": COMPANION + suffix,
        "extractor": f"{DEPENDENCIES}/{EXTRACTOR}{suffix}",
        "manifest": f"{DEPENDENCIES}/bundle-manifest.json",
        "resources": None,
    }


def write_zip(source_dir, archive, prefix=None):
    """Zip a directory tree with fixed times, sorted entries and normalized
    permissions, so the same tree always gives the same bytes.

    `prefix` is the top-level directory name inside the archive (default: the
    tree's own name). Executable files keep their executable bit, everything
    else is 0644; symbolic links are refused, because a release has none and a
    link in an archive is a way to write outside the directory it unpacks to.
    """
    prefix = prefix if prefix is not None else os.path.basename(os.path.normpath(source_dir))
    entries = []
    for root, dirs, files in os.walk(source_dir):
        dirs.sort()
        for name in files:
            entries.append(os.path.join(root, name))
        for name in dirs:
            if os.path.islink(os.path.join(root, name)):
                raise ValueError(f"{os.path.join(root, name)} is a symbolic link; a release carries none")
    entries.sort(key=lambda path: os.path.relpath(path, source_dir).replace(os.sep, "/"))
    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as out:
        for path in entries:
            if os.path.islink(path):
                raise ValueError(f"{path} is a symbolic link; a release carries none")
            relative = os.path.relpath(path, source_dir).replace(os.sep, "/")
            name = f"{prefix}/{relative}" if prefix else relative
            info = zipfile.ZipInfo(name, date_time=ZIP_EPOCH)
            mode = os.stat(path).st_mode
            executable = bool(mode & stat.S_IXUSR) or name.endswith(".exe")
            info.external_attr = ((0o100755 if executable else 0o100644) & 0xFFFF) << 16
            info.create_system = 3  # Unix, so the permission bits above are read back
            info.compress_type = zipfile.ZIP_DEFLATED
            with open(path, "rb") as handle:
                out.writestr(info, handle.read(), compresslevel=9)


def extract_zip(archive, dest):
    """Unpack a release zip, restoring the executable bits `write_zip` stored,
    and refusing any entry that would land outside `dest`."""
    dest_real = os.path.realpath(dest)
    with zipfile.ZipFile(archive) as source:
        for info in source.infolist():
            target = os.path.realpath(os.path.join(dest, info.filename))
            if target != dest_real and not target.startswith(dest_real + os.sep):
                raise ValueError(f"{archive}: {info.filename} would unpack outside {dest}")
            source.extract(info, dest)
            if not info.is_dir():
                mode = (info.external_attr >> 16) & 0o777
                if mode:
                    os.chmod(target, mode)
