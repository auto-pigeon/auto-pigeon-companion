#!/bin/sh
# Refresh the desktop database after the handler's .desktop file has gone.
#
# The user's own configuration, cache and build history are deliberately left
# alone: this script runs as root, and deleting a directory under somebody's
# home because a package was removed is how unrelated data gets lost. The
# per-user command that does remove them is `companion uninstall --purge`, and
# README.md lists the directories.
set -e
if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database /usr/share/applications >/dev/null 2>&1 || true
fi
exit 0
