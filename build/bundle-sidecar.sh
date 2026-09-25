#!/usr/bin/env bash
set -euo pipefail

# Assemble one platform's distribution bundle: the Companion, a bundle
# manifest, and — when one is given — an Auto-Pigeon Extractor build for the
# same platform, beside it. Nothing else: the licences and notices live in the
# repositories, not in the archive (operator decision, 2026-09-25).
#
# # Where the extractor comes from
#
# From --extractor: a binary BUILT BEFOREHAND from the extractor's own
# repository (its scripts/build-release.sh), for exactly this platform. The
# release workflow builds it from the commit `build/aue-pin.json` names and
# passes it here with that commit, the extractor's licence identifier — quoted
# from that build's release manifest, never restated here — and the repository
# it came from. A bundle made without --extractor is complete,
# carries no extractor, and says so in its manifest. Nothing here, and nothing
# in the Companion, downloads a program.
#
# # What this script must never do
#
# Link, embed, or copy extractor SOURCE. Put an extractor inside the Companion's
# own binary. The two programs are two files, under two licences — the
# Companion MIT, the extractor proprietary (NEW_247G) — and the bundle says
# which is which in its manifest, so an archive carrying the extractor is never
# "an MIT archive"; the extractor is copied in as
# `auto-pigeon-extractor[.exe]`, the name the Companion looks for beside itself,
# and its digest in the manifest is what the Companion checks before running it.
#
# usage:
#   build/bundle-sidecar.sh --platform <goos>-<goarch> --version <v> \
#       --binary-dir <dir> --out <dir> \
#       [--extractor <file> --extractor-version <v> --extractor-commit <sha> \
#        [--extractor-spdx <id>] --extractor-source <url>]

PLATFORM=""
VERSION=""
BINARY_DIR=""
OUT=""
EXTRACTOR=""
EXTRACTOR_VERSION=""
EXTRACTOR_SOURCE=""
EXTRACTOR_SPDX="LicenseRef-auto-pigeon-extractor"
EXTRACTOR_COMMIT=""

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

while [ $# -gt 0 ]; do
    case "$1" in
        --platform)   PLATFORM="${2:?--platform needs a value}"; shift 2 ;;
        --version)    VERSION="${2:?--version needs a value}"; shift 2 ;;
        --binary-dir) BINARY_DIR="${2:?--binary-dir needs a value}"; shift 2 ;;
        --out)        OUT="${2:?--out needs a value}"; shift 2 ;;
        --extractor)         EXTRACTOR="${2:?--extractor needs a value}"; shift 2 ;;
        --extractor-version) EXTRACTOR_VERSION="${2:?--extractor-version needs a value}"; shift 2 ;;
        --extractor-source)  EXTRACTOR_SOURCE="${2:?--extractor-source needs a value}"; shift 2 ;;
        --extractor-spdx)    EXTRACTOR_SPDX="${2:?--extractor-spdx needs a value}"; shift 2 ;;
        --extractor-commit)  EXTRACTOR_COMMIT="${2:?--extractor-commit needs a value}"; shift 2 ;;
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
    --extractor "$EXTRACTOR" \
    --extractor-version "$EXTRACTOR_VERSION" \
    --extractor-commit "$EXTRACTOR_COMMIT" \
    --extractor-spdx "$EXTRACTOR_SPDX" \
    --extractor-source "$EXTRACTOR_SOURCE"

echo "== ${BUNDLE}"
