#!/usr/bin/env bash
# Assemble a macOS .app bundle around an already-cross-compiled companion
# binary.
#
# No GUI toolkit means nothing generates a bundle for us, but a bundle is only
# a directory with a known shape, so this script builds it directly — no
# external tool, and it runs on Linux CI as well as on macOS.
#
#   ./build/macos/make-app-bundle.sh \
#       --binary dist/darwin-arm64/companion \
#       --version 0.1.0 \
#       --out dist/darwin-arm64
#
# Produces dist/darwin-arm64/Auto-Pigeon Companion.app and, unless --no-zip is
# passed, a .zip beside it for distribution.
#
# NOT DONE HERE: codesign and notarization. There are no certificates yet, so
# the bundle is unsigned and Gatekeeper will block it on first open until the
# user right-clicks → Open. When a Developer ID exists, the two steps go at the
# end of this script (codesign --deep --options runtime, then notarytool
# submit --wait and stapler staple).

set -euo pipefail

BINARY=""
VERSION="0.0.0-dev"
OUT_DIR="dist"
APP_NAME="Auto-Pigeon Companion"
BUNDLE_ID="com.andreadintino.auto-pigeon-companion"
EXECUTABLE="companion"
MAKE_ZIP=1

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
template="${script_dir}/Info.plist.tmpl"

usage() {
    cat >&2 <<'EOF'
usage: make-app-bundle.sh --binary <path> [--version <v>] [--out <dir>]
                          [--bundle-id <id>] [--no-zip]
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --binary)    BINARY="${2:?--binary needs a path}"; shift 2 ;;
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
rm -rf "$app_dir"
mkdir -p "${app_dir}/Contents/MacOS" "${app_dir}/Contents/Resources"

install -m 0755 "$BINARY" "${app_dir}/Contents/MacOS/${EXECUTABLE}"

copyright="Copyright © $(date +%Y) Andrea D'Intino. All rights reserved."
sed -e "s|@BUNDLE_ID@|${BUNDLE_ID}|g" \
    -e "s|@VERSION@|${VERSION}|g" \
    -e "s|@EXECUTABLE@|${EXECUTABLE}|g" \
    -e "s|@COPYRIGHT@|${copyright}|g" \
    "$template" > "${app_dir}/Contents/Info.plist"

# PkgInfo is legacy but harmless, and some tools still look for it.
printf 'APPL????' > "${app_dir}/Contents/PkgInfo"

echo "built ${app_dir}"

if [ "$MAKE_ZIP" -eq 1 ]; then
    zip_path="${OUT_DIR}/auto-pigeon-companion-${VERSION}.app.zip"
    rm -f "$zip_path"
    if command -v ditto >/dev/null 2>&1; then
        # ditto preserves the bundle's resource forks and metadata; it is the
        # right tool on macOS and the one Apple's notarization docs assume.
        ditto -c -k --keepParent "$app_dir" "$zip_path"
    elif command -v zip >/dev/null 2>&1; then
        (cd "$OUT_DIR" && zip -qry "$(basename "$zip_path")" "${APP_NAME}.app")
    else
        echo "warning: neither ditto nor zip is available; skipping the archive" >&2
        exit 0
    fi
    echo "packaged ${zip_path}"
fi
