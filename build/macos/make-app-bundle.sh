#!/usr/bin/env bash
# Assemble a macOS .app bundle around an already cross-compiled companion
# binary.
#
# The Companion has no GUI toolkit, so nothing generates this bundle for us: a
# .app is a directory with a known layout, and this script writes that layout.
# It runs on any platform — it is `mkdir`, `cp`, and `sed` — which is what lets
# the whole release be produced from one runner without a Mac.
#
# Usage:
#   build/macos/make-app-bundle.sh --binary <path> [--arch <goarch>]
#                                  [--version <v>] [--out <dir>]
#                                  [--bundle-id <id>] [--no-zip]
#
# Example, from the repository root:
#   GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build \
#     -ldflags "-X main.version=0.1.0" \
#     -o dist/darwin-arm64/companion ./cmd/companion
#   build/macos/make-app-bundle.sh --binary dist/darwin-arm64/companion \
#     --arch arm64 --version 0.1.0 --out dist/darwin-arm64
#
# Produces "<out>/Auto-Pigeon Companion.app" and, unless --no-zip is passed, a
# .zip beside it.
#
# NOT signed and NOT notarized. No certificate or Apple Developer credentials
# exist, so an unsigned bundle is what this produces and Gatekeeper will warn on
# first launch. Signing is a documented release step in README.md, not an
# implemented one — see the note at the end of this script.

set -euo pipefail

BINARY=""
ARCH="$(uname -m)"
VERSION="0.0.0-dev"
OUT_DIR="dist"
APP_NAME="Auto-Pigeon Companion"
BUNDLE_ID="io.github.andrea-dintino.auto-pigeon-companion"
EXECUTABLE="companion"
MAKE_ZIP=1

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
template="${script_dir}/Info.plist.tmpl"

usage() {
    cat >&2 <<'USAGE'
usage: make-app-bundle.sh --binary <path> [--arch <goarch>] [--version <v>]
                          [--out <dir>] [--bundle-id <id>] [--no-zip]
USAGE
}

while [ $# -gt 0 ]; do
    case "$1" in
        --binary)    BINARY="${2:?--binary needs a path}"; shift 2 ;;
        --arch)      ARCH="${2:?--arch needs a value}"; shift 2 ;;
        --version)   VERSION="${2:?--version needs a value}"; shift 2 ;;
        --out)       OUT_DIR="${2:?--out needs a directory}"; shift 2 ;;
        --bundle-id) BUNDLE_ID="${2:?--bundle-id needs a value}"; shift 2 ;;
        --no-zip)    MAKE_ZIP=0; shift ;;
        -h|--help)   usage; exit 0 ;;
        *)           echo "error: unknown argument: $1" >&2; usage; exit 2 ;;
    esac
done

if [ -z "$BINARY" ]; then
    echo "error: --binary is required" >&2
    usage
    exit 2
fi
if [ ! -f "$BINARY" ]; then
    echo "error: binary not found: $BINARY" >&2
    exit 2
fi
if [ ! -f "$template" ]; then
    echo "error: Info.plist template not found: $template" >&2
    exit 1
fi

app_dir="${OUT_DIR}/${APP_NAME}.app"
# A stale bundle from a previous run would keep files this one no longer emits.
rm -rf "$app_dir"
mkdir -p "${app_dir}/Contents/MacOS" "${app_dir}/Contents/Resources"

# The executable name must match CFBundleExecutable in the template.
install -m 0755 "$BINARY" "${app_dir}/Contents/MacOS/${EXECUTABLE}"

# Licensing: both files ship inside the bundle. THIRD_PARTY_NOTICES.md states
# that the external map-building tools are separate GPL-2.0 programs which are
# downloaded at runtime and are not part of this bundle. If a tool binary is
# ever bundled here instead, its own license text has to ship beside it.
cp "${repo_root}/LICENSE" "${app_dir}/Contents/Resources/LICENSE"
cp "${repo_root}/THIRD_PARTY_NOTICES.md" "${app_dir}/Contents/Resources/THIRD_PARTY_NOTICES.md"

copyright="Copyright © $(date +%Y) Andrea D'Intino. MIT licensed; see LICENSE. External map-building tools are separate GPL-2.0 programs — see THIRD_PARTY_NOTICES.md."
sed -e "s|@BUNDLE_ID@|${BUNDLE_ID}|g" \
    -e "s|@VERSION@|${VERSION}|g" \
    -e "s|@ARCH@|${ARCH}|g" \
    -e "s|@EXECUTABLE@|${EXECUTABLE}|g" \
    -e "s|@COPYRIGHT@|${copyright}|g" \
    "$template" > "${app_dir}/Contents/Info.plist"

# PkgInfo is legacy but harmless, and some tooling still looks for it.
printf 'APPL????' > "${app_dir}/Contents/PkgInfo"

echo "built ${app_dir}"

if [ "$MAKE_ZIP" -eq 1 ]; then
    zip_path="${OUT_DIR}/auto-pigeon-companion-${VERSION}-darwin-${ARCH}.zip"
    rm -f "$zip_path"
    if command -v ditto >/dev/null 2>&1; then
        # ditto preserves the bundle's resource forks and extended attributes;
        # on macOS it is the correct way to zip a .app and the one Apple's
        # notarization documentation assumes.
        ditto -c -k --sequesterRsrc --keepParent "$app_dir" "$zip_path"
    elif command -v zip >/dev/null 2>&1; then
        # Cross-building from Linux: plain zip. Nothing in this bundle uses
        # extended attributes, so the archive is equivalent.
        (cd "$OUT_DIR" && zip -qry "$(basename "$zip_path")" "${APP_NAME}.app")
    else
        echo "warning: neither ditto nor zip is available; skipping the archive" >&2
        exit 0
    fi
    echo "packaged ${zip_path}"
fi

# TODO(andrea): code signing and notarization. Once an Apple Developer ID
# exists, the steps that belong here are:
#
#   codesign --deep --force --options runtime --timestamp \
#     --sign "Developer ID Application: <name> (<team id>)" "${app_dir}"
#   xcrun notarytool submit "${zip_path}" --apple-id <id> --team-id <team> \
#     --password <app-specific-password> --wait
#   xcrun stapler staple "${app_dir}"
#
# All three require a real certificate and only run on macOS, so none is
# implemented. Until then the release is unsigned and users see a Gatekeeper
# warning.
