#!/usr/bin/env bash
set -euo pipefail

# Assemble one platform's distribution bundle: the Companion, a bundle manifest,
# and — when one is given — the auto-pigeon-mapio converter for the same platform
# beside it, with its notices. The licences and notices of everything else live in
# the repositories, not in the archive (operator decision, 2026-09-25); the
# converter's are here because the library in it is redistributed in binary form,
# and its licence asks for them to travel with it (NEW_371).
#
# # Where the converter comes from
#
# From --converter: a binary BUILT BEFOREHAND by build/release.sh, from cmd/mapio in
# this commit and the go/mapio revision go.mod pins, for exactly this platform. A
# bundle made without --converter is complete, carries no converter, and says so in
# its manifest. Nothing here, and nothing in the Companion, downloads a program.
#
# # What this script must never do
#
# Put the converter inside the Companion's own binary. The two programs are two
# files, under different licences — the Companion MIT, the converter Apache-2.0 AND
# MIT — and the bundle says which is which in its manifest, so an archive carrying
# the converter is never "an MIT archive"; it is copied in as
# `dependencies/auto-pigeon-mapio[.exe]` (inside the .app on macOS), where the
# Companion looks for it, and its digest in the manifest is what the Companion
# checks before running it.
#
# usage:
#   build/bundle-sidecar.sh --platform <goos>-<goarch> --version <v> \
#       --binary-dir <dir> --out <dir> \
#       [--converter <file> --converter-version <v> --converter-commit <sha> \
#        --converter-source <url> --converter-notices <file> \
#        --library-version <v> --library-commit <sha> [--converter-spdx <id>]]

PLATFORM=""
VERSION=""
BINARY_DIR=""
OUT=""
CONVERTER=""
CONVERTER_VERSION=""
CONVERTER_SOURCE=""
CONVERTER_SPDX="Apache-2.0 AND MIT"
CONVERTER_COMMIT=""
CONVERTER_NOTICES=""
LIBRARY_VERSION=""
LIBRARY_COMMIT=""

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

while [ $# -gt 0 ]; do
    case "$1" in
        --platform)   PLATFORM="${2:?--platform needs a value}"; shift 2 ;;
        --version)    VERSION="${2:?--version needs a value}"; shift 2 ;;
        --binary-dir) BINARY_DIR="${2:?--binary-dir needs a value}"; shift 2 ;;
        --out)        OUT="${2:?--out needs a value}"; shift 2 ;;
        --converter)         CONVERTER="${2:?--converter needs a value}"; shift 2 ;;
        --converter-version) CONVERTER_VERSION="${2:?--converter-version needs a value}"; shift 2 ;;
        --converter-source)  CONVERTER_SOURCE="${2:?--converter-source needs a value}"; shift 2 ;;
        --converter-spdx)    CONVERTER_SPDX="${2:?--converter-spdx needs a value}"; shift 2 ;;
        --converter-commit)  CONVERTER_COMMIT="${2:?--converter-commit needs a value}"; shift 2 ;;
        --converter-notices) CONVERTER_NOTICES="${2:?--converter-notices needs a value}"; shift 2 ;;
        --library-version)   LIBRARY_VERSION="${2:?--library-version needs a value}"; shift 2 ;;
        --library-commit)    LIBRARY_COMMIT="${2:?--library-commit needs a value}"; shift 2 ;;
        -h|--help)    sed -n '/^# usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *)            echo "error: unknown argument: $1" >&2; exit 2 ;;
    esac
done

if [ -z "$PLATFORM" ] || [ -z "$VERSION" ] || [ -z "$BINARY_DIR" ] || [ -z "$OUT" ]; then
    echo "error: --platform, --version, --binary-dir and --out are all required" >&2
    exit 2
fi
if [ ! -d "$BINARY_DIR" ]; then
    echo "error: ${BINARY_DIR} does not exist; build the Companion first" >&2
    exit 2
fi

BUNDLE="${OUT}/auto-pigeon-companion-${VERSION}-${PLATFORM}"
rm -rf "$BUNDLE"
mkdir -p "$BUNDLE"

# The Companion, and its macOS app bundle when there is one.
cp -R "${BINARY_DIR}/." "$BUNDLE/"

python3 "${repo_root}/build/bundle-manifest.py" \
    --platform "$PLATFORM" \
    --version "$VERSION" \
    --bundle "$BUNDLE" \
    --converter "$CONVERTER" \
    --converter-version "$CONVERTER_VERSION" \
    --converter-commit "$CONVERTER_COMMIT" \
    --converter-spdx "$CONVERTER_SPDX" \
    --converter-source "$CONVERTER_SOURCE" \
    --converter-notices "$CONVERTER_NOTICES" \
    --library-version "$LIBRARY_VERSION" \
    --library-commit "$LIBRARY_COMMIT"

echo "== ${BUNDLE}"
