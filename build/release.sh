#!/usr/bin/env bash
# Build a release of Auto-Pigeon Companion: every supported target, the macOS
# bundles, an SBOM, and a checksum file.
#
# Usage, from the repository root:
#
#   build/release.sh --version 0.2.0
#   build/release.sh --version 0.2.0 --out dist/0.2.0 --targets linux/amd64,windows/amd64
#
# # This script signs nothing, and holds no key
#
# There is no Apple Developer ID and no Authenticode certificate for this
# project, so what it produces is unsigned: macOS shows a Gatekeeper warning and
# Windows shows SmartScreen on first run. The procedures for the day a
# certificate exists are in README.md, under "Releasing", and in
# build/macos/make-app-bundle.sh. `companion security residual` states what
# being unsigned currently means, with an owner and a review date.
#
# What a downloader can check today is SHA256SUMS, published beside the
# artifacts. That is not a signature — anybody who can replace the artifacts can
# replace the checksum file — but it is what makes a corrupted or truncated
# download detectable, and it is honest about being no more than that.
#
# # Determinism
#
# -trimpath and an explicit -ldflags, with CGO off, so a rebuild of the same
# commit produces the same bytes. The SBOM carries no timestamp unless
# --timestamp is passed, for the same reason: a document that changed on every
# run could not be published beside a digest of itself.
set -euo pipefail

VERSION=""
OUT="dist"
TIMESTAMP=""
TARGETS="windows/amd64,windows/arm64,linux/amd64,linux/arm64,darwin/amd64,darwin/arm64"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
    sed -n '2,30p' "${BASH_SOURCE[0]}" >&2
}

while [ $# -gt 0 ]; do
    case "$1" in
        --version)   VERSION="${2:?--version needs a value}"; shift 2 ;;
        --out)       OUT="${2:?--out needs a directory}"; shift 2 ;;
        --targets)   TARGETS="${2:?--targets needs a list}"; shift 2 ;;
        --timestamp) TIMESTAMP="${2:?--timestamp needs an RFC3339 time}"; shift 2 ;;
        -h|--help)   usage; exit 0 ;;
        *)           echo "error: unknown argument: $1" >&2; usage; exit 2 ;;
    esac
done

if [ -z "$VERSION" ]; then
    echo "error: --version is required" >&2
    usage
    exit 2
fi

cd "$repo_root"
mkdir -p "$OUT"

echo "== building $VERSION into $OUT =="
IFS=',' read -r -a targets <<< "$TARGETS"
for target in "${targets[@]}"; do
    goos="${target%%/*}"
    goarch="${target##*/}"
    ext=""
    [ "$goos" = "windows" ] && ext=".exe"

    staging="$OUT/stage/${goos}-${goarch}"
    mkdir -p "$staging"
    echo "-- ${goos}/${goarch}"
    GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build \
        -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o "${staging}/companion${ext}" \
        ./cmd/companion

    # The licence files travel with every artifact. THIRD_PARTY_NOTICES.md is
    # the statement that the GPL compilers and engines, and the AGPL extractor,
    # are separate programs obtained separately — which is only true if a person
    # who downloads this can read it.
    cp LICENSE THIRD_PARTY_NOTICES.md "$staging/"

    case "$goos" in
        darwin)
            build/macos/make-app-bundle.sh \
                --binary "${staging}/companion" \
                --arch "$goarch" --version "$VERSION" --out "$staging" --no-zip
            ( cd "$staging" && zip -qry "../../auto-pigeon-companion-${VERSION}-darwin-${goarch}.zip" \
                "Auto-Pigeon Companion.app" LICENSE THIRD_PARTY_NOTICES.md )
            ;;
        windows)
            ( cd "$staging" && zip -qr "../../auto-pigeon-companion-${VERSION}-windows-${goarch}.zip" . )
            ;;
        *)
            tar -czf "$OUT/auto-pigeon-companion-${VERSION}-${goos}-${goarch}.tar.gz" \
                -C "$staging" .
            ;;
    esac
done

# The SBOM comes from the program, not from a file beside it: an SBOM somebody
# maintains by hand is one that is wrong within two changes. It is generated
# with the host toolchain because it only has to READ this module's graph, which
# is the same graph every cross-built artifact has.
echo "== SBOM =="
sbom_args=(release sbom --out "$OUT/auto-pigeon-companion-${VERSION}.cdx.json")
[ -n "$TIMESTAMP" ] && sbom_args+=(--timestamp "$TIMESTAMP")
go run -ldflags "-X main.version=${VERSION}" ./cmd/companion "${sbom_args[@]}"

echo "== checksums =="
rm -rf "$OUT/stage"
go run ./cmd/companion release checksums --dir "$OUT" --out -

echo
echo "Built $VERSION in $OUT. NOTHING HERE IS SIGNED — see the header of this"
echo "script and 'companion security residual' for what that means."
