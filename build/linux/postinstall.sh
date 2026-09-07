#!/bin/sh
# Refresh the desktop database so the autopigeon:// handler this package
# installed is seen without a logout.
#
# Best effort and nothing else. xdg-utils is not a dependency — the Companion
# runs fine without it and prints its own URL — so a machine that does not have
# it must still install cleanly. No user data is touched here: see nfpm.yaml.
set -e
if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database /usr/share/applications >/dev/null 2>&1 || true
fi
exit 0
