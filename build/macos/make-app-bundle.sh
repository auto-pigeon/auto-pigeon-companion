#!/usr/bin/env bash
# Assemble a macOS .app bundle around an already cross-compiled binary.
#
# AUL has no GUI toolkit, so nothing generates this bundle for us: a .app is a
# directory with a known layout, and this script writes that layout. It runs on
# any platform — it is `mkdir`, `cp`, and `sed` — which is what lets the whole
# release be produced from one runner without a Mac.
#
# Usage:
#   build/macos/make-app-bundle.sh <binary> <arch> <version> [output-dir]
#
# Example, from the repository root:
#   GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build \
#     -ldflags "-X main.version=0.1.0" \
#     -o dist/darwin-arm64/auto-pigeon-launcher ./cmd/launcher
#   build/macos/make-app-bundle.sh dist/darwin-arm64/auto-pigeon-launcher arm64 0.1.0 dist
#
# Produces dist/Auto-Pigeon Launcher.app and dist/auto-pigeon-launcher-0.1.0-darwin-arm64.zip
#
# NOT signed and NOT notarized. No certificate or Apple Developer credentials
# exist, so an unsigned bundle is what this produces, and Gatekeeper will warn
# on first launch. Signing is a documented release step in README.md, not an
# implemented one — see the note at the end of this script.

set -euo pipefail

if [ "$#" -lt 3 ]; then
	echo "usage: $0 <binary> <arch> <version> [output-dir]" >&2
	exit 2
fi

binary="$1"
arch="$2"
version="$3"
output_dir="${4:-dist}"

script_dir="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
template="${script_dir}/Info.plist.tmpl"

if [ ! -f "${binary}" ]; then
	echo "error: no binary at ${binary}" >&2
	exit 1
fi
if [ ! -f "${template}" ]; then
	echo "error: no Info.plist template at ${template}" >&2
	exit 1
fi

app_name="Auto-Pigeon Launcher"
bundle="${output_dir}/${app_name}.app"

# A stale bundle from a previous run would keep files this one no longer emits.
rm -rf "${bundle}"
mkdir -p "${bundle}/Contents/MacOS" "${bundle}/Contents/Resources"

# The executable name must match CFBundleExecutable in the template.
cp "${binary}" "${bundle}/Contents/MacOS/auto-pigeon-launcher"
chmod 755 "${bundle}/Contents/MacOS/auto-pigeon-launcher"

# Licensing: both files ship inside the bundle. THIRD_PARTY_NOTICES.md states
# that the external map-building tools are separate GPL-2.0 programs which are
# downloaded at runtime and are not part of this bundle. If a tool binary is
# ever bundled here instead, its own license text has to ship beside it.
cp "${repo_root}/LICENSE" "${bundle}/Contents/Resources/LICENSE"
cp "${repo_root}/THIRD_PARTY_NOTICES.md" "${bundle}/Contents/Resources/THIRD_PARTY_NOTICES.md"

sed -e "s/@VERSION@/${version}/g" -e "s/@ARCH@/${arch}/g" \
	"${template}" >"${bundle}/Contents/Info.plist"

# PkgInfo is legacy but harmless, and some tooling still looks for it.
printf 'APPL????' >"${bundle}/Contents/PkgInfo"

archive="${output_dir}/auto-pigeon-launcher-${version}-darwin-${arch}.zip"
rm -f "${archive}"
if command -v ditto >/dev/null 2>&1; then
	# ditto preserves the bundle's resource forks and extended attributes;
	# on macOS it is the correct way to zip a .app.
	ditto -c -k --sequesterRsrc --keepParent "${bundle}" "${archive}"
else
	# Cross-building from Linux: plain zip. Nothing in this bundle uses
	# extended attributes, so the archive is equivalent.
	(cd "${output_dir}" && zip -qry "$(basename "${archive}")" "${app_name}.app")
fi

echo "built ${bundle}"
echo "built ${archive}"

# TODO(andrea): code signing and notarization. Once an Apple Developer ID
# exists, the two steps that belong here are:
#
#   codesign --deep --force --options runtime --timestamp \
#     --sign "Developer ID Application: <name> (<team id>)" "${bundle}"
#   xcrun notarytool submit "${archive}" --apple-id <id> --team-id <team> \
#     --password <app-specific-password> --wait
#   xcrun stapler staple "${bundle}"
#
# Both require a real certificate and both only run on macOS, so neither is
# implemented. Until then the release is unsigned and users see a Gatekeeper
# warning.
