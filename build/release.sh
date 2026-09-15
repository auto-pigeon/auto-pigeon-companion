#!/usr/bin/env bash
# Build a release of Auto-Pigeon Companion: every supported target, the macOS
# bundles, an SBOM, and a checksum file.
#
# Usage, from the repository root:
#
#   build/release.sh
#   build/release.sh --out dist/1.842 --targets linux/amd64,windows/amd64
#
# # The version is 1.<commit-count>
#
# The format AUP and AUG report: `1.` followed by `git rev-list --count HEAD`
# in this repository, derived here rather than typed. `--version` is accepted
# only to restate that number (a CI job that already computed it); any other
# shape is refused, because a second version string for one commit is exactly
# what the format exists to prevent. A tree with uncommitted changes is built,
# and `companion version` says `modified` — the source commit and that fact are
# in the binary's own build information.
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
    sed -n '2,42p' "${BASH_SOURCE[0]}" >&2
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

cd "$repo_root"

count="$(git rev-list --count HEAD 2>/dev/null || true)"
if [ -z "$count" ]; then
    echo "error: cannot count this repository's commits; the version is 1.<commit-count> and needs the Git history" >&2
    exit 2
fi
derived="1.${count}"
if [ -z "$VERSION" ]; then
    VERSION="$derived"
elif [ "$VERSION" != "$derived" ]; then
    echo "error: --version $VERSION is not this commit's version ($derived = 1.<git rev-list --count HEAD>)" >&2
    exit 2
fi
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
    echo "warning: the working tree has uncommitted changes; the artifacts will say 'modified'" >&2
fi
mkdir -p "$OUT"

# # The About content is a release gate — NEW_243E_AUT_AUP_AUG_AUCOM §B
#
# internal/web/assets/about.json is a GENERATED copy of auto-pigeon-gallery/content/about.md, and every
# artifact embeds it. In a workspace checkout the canonical comparison runs (read-only) and a stale,
# hand-edited or unmarked copy refuses the release. Without the workspace, the copy must still say it
# is generated and carry every image it names, and the release says the comparison was not made.
about_command="$repo_root/../auto-pigeon-tools/scripts/about-content.sh"
if [ -f "$about_command" ]; then
    echo "== about content: auto-pigeon-tools/scripts/about-content.sh check =="
    bash "$about_command" check || { echo "error: the About content gate refused this release" >&2; exit 1; }
else
    echo "== about content: NOT COMPARED with the canonical file (no auto-pigeon-tools beside this checkout) =="
    go test ./internal/web -run 'TestTheEmbeddedAboutIsTheGeneratedCopy' -count=1
fi

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

    # The native acceptance kit travels with every artifact too, and for a
    # reason the licence files do not have: an artifact for a platform nobody
    # can run is `build_only` until somebody runs it, and the only person who
    # can is whoever has that machine. Shipping the kit inside the download is
    # what makes "run this and send the bundle back" a single instruction
    # instead of a checkout, a toolchain and a set of paths.
    #
    # Both entry points ship in every artifact. A Linux machine with pwsh on it
    # is a real machine, a Windows machine with a POSIX shell is a real
    # machine, and shipping only the one that matches the target would make the
    # kit unusable on either.
    cp acceptance/run-acceptance.sh acceptance/run-acceptance.ps1 \
       acceptance/kit-options.json "$staging/"
    chmod +x "$staging/run-acceptance.sh"

    # run-acceptance.sh / .ps1 verify the program against a SHA256SUMS BESIDE
    # it, before starting it. With none inside the archive, every operator's
    # bundle recorded `checksums: not_available` (NEW_244D rehearsal). It is
    # corruption evidence for the unpacked files; the archive's own digest is in
    # the SHA256SUMS published beside the archives, and neither is a signature.
    # macOS gets none: its binary moves inside the .app bundle below.
    if [ "$goos" != "darwin" ]; then
        go run ./cmd/companion release checksums --dir "$staging" --out - >/dev/null
    fi

    case "$goos" in
        darwin)
            build/macos/make-app-bundle.sh \
                --binary "${staging}/companion" \
                --arch "$goarch" --version "$VERSION" --out "$staging" --no-zip
            ( cd "$staging" && zip -qry "../../auto-pigeon-companion-${VERSION}-darwin-${goarch}.zip" \
                "Auto-Pigeon Companion.app" LICENSE THIRD_PARTY_NOTICES.md \
                run-acceptance.sh run-acceptance.ps1 kit-options.json )
            ;;
        windows)
            ( cd "$staging" && zip -qr "../../auto-pigeon-companion-${VERSION}-windows-${goarch}.zip" . )
            ;;
        *)
            tar -czf "$OUT/auto-pigeon-companion-${VERSION}-${goos}-${goarch}.tar.gz" \
                -C "$staging" .
            ;;
    esac

    # The kit's checksum lane is only as good as the file it reads. Checked here
    # rather than trusted: an archive that shipped without it is refused.
    if [ "$goos" != "darwin" ] && [ ! -f "$staging/SHA256SUMS" ] || \
       { [ "$goos" != "darwin" ] && ! grep -q "  companion${ext}\$" "$staging/SHA256SUMS"; }; then
        echo "error: ${goos}/${goarch}: no SHA256SUMS line for companion${ext} beside the program" >&2
        exit 1
    fi
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
echo "== what this release CLAIMS =="
go run ./cmd/companion release support

echo
echo "Built $VERSION in $OUT. NOTHING HERE IS SIGNED — see the header of this"
echo "script and 'companion security residual' for what that means."
echo
echo "${#targets[@]} artifact(s) were BUILT (${TARGETS}). What each of them is VERIFIED to do on its own"
echo "hardware is the table above, and it does not change because a build"
echo "succeeded: run acceptance/run-acceptance.sh (or .ps1) on the machine and"
echo "send the bundle back."
