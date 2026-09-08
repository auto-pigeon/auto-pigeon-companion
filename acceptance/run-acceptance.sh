#!/bin/sh
# Auto-Pigeon Companion — the native operator acceptance kit, POSIX half.
#
# Run this on the machine the artifact is for. It produces a small result
# bundle you can send back; nothing is uploaded from here.
#
#   ./run-acceptance.sh
#   ./run-acceptance.sh --tool-path /opt/ericw-tools --out ~/aucom-bundle
#   ./run-acceptance.sh --engine /opt/quakespasm/quakespasm --game-root ~/Quake
#
# # What this script is for, and what it deliberately is not
#
# The lanes are in the program, once, so a Windows run and a Linux run are the
# same run. Two hand-written harnesses in two shell languages would be two
# contracts that agree until the day they do not.
#
# What is HERE is the half a program cannot do for itself: verify the
# artifact's checksums BEFORE starting it, and read what the operating system
# calls this machine — so an amd64 binary under emulation is not reported as
# native arm64 evidence.
#
# It is `sh`, not `bash`: a POSIX shell is what a minimal Linux image, a BSD and
# a macOS install all have.
#
# # It never touches your game data
#
# --game-root is passed through and nothing else. No lane copies, archives,
# hashes wholesale or uploads a byte of it, and there is no option here that
# makes one.
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

companion=""
out=""
no_checksums=0
tool_path=""
engine=""
game_root=""
game_family=""
only=""
skip=""

usage() {
    printf 'usage: run-acceptance.sh [options]\n\n' >&2
    printf '  --out <dir>           where the result bundle is written\n' >&2
    printf '  --tool-path <dir>     the ROOT of an EricW build you already have\n' >&2
    printf '  --engine <path>       an engine executable you already have\n' >&2
    printf '  --game-root <dir>     a game installation you already own\n' >&2
    printf '  --game-family <name>  quake1 (default), quake2 or quake3\n' >&2
    printf '  --only <a,b>          run only these lanes\n' >&2
    printf '  --skip <a,b>          run everything but these lanes\n' >&2
    printf '  --companion <path>    the Companion to run\n' >&2
    printf '  --no-checksums        skip the checksum check and RECORD that it was skipped\n' >&2
    printf '  --help                this list\n\n' >&2
    printf 'No option here copies, archives, hashes or uploads game data.\n' >&2
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --out)          out=${2:?--out needs a directory}; shift 2 ;;
        --tool-path)    tool_path=${2:?--tool-path needs a directory}; shift 2 ;;
        --engine)       engine=${2:?--engine needs a path}; shift 2 ;;
        --game-root)    game_root=${2:?--game-root needs a directory}; shift 2 ;;
        --game-family)  game_family=${2:?--game-family needs a name}; shift 2 ;;
        --only)         only=${2:?--only needs a lane list}; shift 2 ;;
        --skip)         skip=${2:?--skip needs a lane list}; shift 2 ;;
        --companion)    companion=${2:?--companion needs a path}; shift 2 ;;
        --no-checksums) no_checksums=1; shift ;;
        -h|--help)      usage; exit 0 ;;
        *)              printf 'error: unknown option: %s\n' "$1" >&2; usage; exit 2 ;;
    esac
done

[ -n "$companion" ] || companion="$script_dir/companion"
if [ ! -x "$companion" ]; then
    printf 'error: no Companion at %s\n' "$companion" >&2
    printf 'Unpack the release artifact and run this script from inside it, or pass --companion <path>.\n' >&2
    exit 2
fi
[ -n "$out" ] || out="$script_dir/acceptance-bundle"

# --- the half a program cannot do for itself --------------------------------
#
# A binary cannot vouch for its own bytes: by the time it is running, whatever
# was going to happen has happened. So the check is here, it runs BEFORE the
# program starts, and its answer is passed in as a fact rather than assumed.
# A machine with no digest tool records `not_available`, never `pass`.
digest_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum -- "$1" | cut -d' ' -f1
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 -- "$1" | cut -d' ' -f1
    elif command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 -- "$1" | awk '{print $NF}'
    else
        return 1
    fi
}

sums="$(dirname -- "$companion")/SHA256SUMS"
checksums="not_available"
checksum_detail="no SHA256SUMS beside the artifact"
if [ "$no_checksums" -eq 1 ]; then
    checksum_detail="the operator passed --no-checksums, so nothing was verified"
elif [ -f "$sums" ]; then
    if actual=$(digest_of "$companion"); then
        name=$(basename -- "$companion")
        expected=$(awk -v want="$name" '$2 == want || $2 == "*" want { print $1 }' "$sums" | head -n 1)
        if [ -z "$expected" ]; then
            checksum_detail="SHA256SUMS names no line for this artifact"
        elif [ "$expected" = "$actual" ]; then
            checksums="pass"
            checksum_detail="the artifact matches its published SHA256SUMS line"
        else
            checksums="fail"
            checksum_detail="the artifact does NOT match its published SHA256SUMS line"
        fi
    else
        checksum_detail="this machine has no sha256sum, shasum or openssl"
    fi
fi

if [ "$checksums" = "fail" ]; then
    printf 'error: %s\n' "$checksum_detail" >&2
    printf 'Do not run an artifact whose bytes are not the published ones. Download it again.\n' >&2
    exit 1
fi

# What the operating system calls this machine. Nothing the program can see
# would tell it that an amd64 artifact is running on an arm64 machine.
host_arch=$(uname -m 2>/dev/null || echo unknown)
shell_id="$(uname -s 2>/dev/null || echo unknown) POSIX sh"

set -- acceptance run --out "$out" \
    --entry-point posix --shell "$shell_id" --host-arch "$host_arch" \
    --checksums "$checksums" --checksum-detail "$checksum_detail"
[ -z "$tool_path" ]   || set -- "$@" --tool-path "$tool_path"
[ -z "$engine" ]      || set -- "$@" --engine "$engine"
[ -z "$game_root" ]   || set -- "$@" --game-root "$game_root"
[ -z "$game_family" ] || set -- "$@" --game-family "$game_family"
[ -z "$only" ]        || set -- "$@" --only "$only"
[ -z "$skip" ]        || set -- "$@" --skip "$skip"

printf '== Auto-Pigeon Companion native acceptance ==\n'
printf 'artifact   %s\n' "$companion"
printf 'checksums  %s (%s)\n' "$checksums" "$checksum_detail"
printf 'machine    %s\n\n' "$host_arch"

status=0
"$companion" "$@" || status=$?

printf '\nSend back the whole of %s.\n' "$out"
printf 'It holds no game data, no credential, no log and no absolute path;\n'
printf 'check that for yourself with:  %s acceptance verify %s\n' "$companion" "$out"
exit "$status"
