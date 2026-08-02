package web

import (
	"embed"
	"io/fs"
)

// assets is the frontend: plain HTML, CSS, and JavaScript with no build step,
// compiled into the binary so the GUI ships as part of the single executable.
// Editing a file under assets/ and rebuilding is the entire frontend workflow.
//
//go:embed assets
var assets embed.FS

// assetsFS strips the "assets/" prefix so the file server maps "/" to
// assets/index.html rather than "/assets/index.html".
func assetsFS() fs.FS {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		// Unreachable: the embed directive above guarantees the directory
		// exists at compile time.
		panic(err)
	}
	return sub
}
