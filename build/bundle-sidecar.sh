#!/usr/bin/env bash
set -euo pipefail

# Assemble one platform's distribution bundle: the Companion, the licences and
# notices, a bundle manifest, and — when a sidecar is PINNED — the matching
# published Auto-Pigeon Extractor release asset.
#
# # What a pin is, and why nothing happens without one
#
# `build/sidecar-pin.json` declares the exact extractor version, the exact URL
# of each platform's asset, and the exact SHA-256 those bytes must have. All
# three, or none: an asset fetched from a branch head or from a `latest` URL is
# an asset nobody can state the contents of in advance, and a bundle whose
# contents cannot be stated is a bundle whose manifest is fiction.
#
# With the pin disabled this script still produces a complete bundle — the
# Companion, both licences, the manifest — and says in the manifest and on
# stdout that no extractor is inside it. The Companion then obtains one the way
# it always has: against the signed catalogue, verified, as a managed install.
# That path is not a fallback for the bundle; the bundle is a convenience over
# it, and `internal/aue` still has exactly two ways to an extractor.
#
# # What this script must never do
#
# Link, embed, or copy extractor SOURCE. Put an unverified executable anywhere
# the Companion would search. Accept a digest mismatch. Rename an extractor so
# it looks like part of the Companion. The two programs are two files, under two
# licences, and the bundle says which is which.
#
# usage:
#   build/bundle-sidecar.sh --platform <goos>-<goarch> --version <v> \
#       --binary-dir <dir> --out <dir> [--pin <file>]

PLATFORM=""
VERSION=""
BINARY_DIR=""
OUT=""
PIN=""

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

while [ $# -gt 0 ]; do
    case "$1" in
        --platform)   PLATFORM="${2:?--platform needs a value}"; shift 2 ;;
        --version)    VERSION="${2:?--version needs a value}"; shift 2 ;;
        --binary-dir) BINARY_DIR="${2:?--binary-dir needs a value}"; shift 2 ;;
        --out)        OUT="${2:?--out needs a value}"; shift 2 ;;
        --pin)        PIN="${2:?--pin needs a value}"; shift 2 ;;
        -h|--help)    sed -n '/^# usage:/,/^$/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
        *)            echo "error: unknown argument: $1" >&2; exit 2 ;;
    esac
done

if [ -z "$PLATFORM" ] || [ -z "$VERSION" ] || [ -z "$BINARY_DIR" ] || [ -z "$OUT" ]; then
    echo "error: --platform, --version, --binary-dir and --out are all required" >&2
    exit 2
fi
if [ -z "$PIN" ]; then
    PIN="${repo_root}/build/sidecar-pin.json"
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

# Licences and notices. BOTH, always: the Companion is MIT and the extractor is
# AGPL-3.0, and a user must be able to tell whose bytes they are running even
# when the bundle carries only one of the two programs.
cp "${repo_root}/LICENSE" "${BUNDLE}/LICENSE-auto-pigeon-companion.txt"
cp "${repo_root}/THIRD_PARTY_NOTICES.md" "${BUNDLE}/THIRD_PARTY_NOTICES.md"

python3 "${repo_root}/build/bundle-manifest.py" \
    --platform "$PLATFORM" \
    --version "$VERSION" \
    --bundle "$BUNDLE" \
    --pin "$PIN"

echo "== ${BUNDLE}"
