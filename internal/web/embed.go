package web

import "embed"

// assets is the frontend, compiled into the binary.
//
// //go:embed is the entire "build step" for the GUI: the frontend is plain
// HTML, CSS and vanilla JS with no bundler, no npm, and no node_modules, so
// there is nothing to compile and nothing to ship beside the binary. A single
// self-contained executable is the point — a user downloads one file and it
// works.
//
// Note what is *not* embedded: no external map-building tool binary, ever. Those
// are GPL-2.0 and are fetched as separate programs at runtime — see
// internal/acquire and THIRD_PARTY_NOTICES.md.
//
//go:embed assets
var assets embed.FS
