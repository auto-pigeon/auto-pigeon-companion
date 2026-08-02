package aue

import "embed"

// BUILD ORDERING CONSTRAINT — READ BEFORE CHANGING THE BUILD.
//
// The AUE binary is not committed to this repository. The build script must
// copy the AUE binary matching the *target* GOOS/GOARCH into
// internal/aue/embedded/ immediately before running `go build` for that
// target, and clear the directory again afterwards:
//
//	cp "$AUE_BIN_FOR_TARGET" internal/aue/embedded/auto-pigeon-extractor
//	GOOS=windows GOARCH=amd64 go build ./cmd/companion
//	rm -f internal/aue/embedded/auto-pigeon-extractor
//
// //go:embed resolves at compile time, so a stale or wrong-platform file left
// in that directory silently ships inside the binary and only fails at
// runtime, on the user's machine. Exactly one non-.gitkeep file may be present
// at any time; Runner rejects anything else rather than guessing.
//
// The directory is otherwise empty (just .gitkeep), so a plain `go build`
// during development produces a companion binary with no embedded AUE. That is
// a supported development state: the runner reports ErrNoEmbeddedBinary, and
// the AUC_AUE_BINARY override (see runner.go) lets a developer point at a
// locally built AUE instead.
//
// The `embedded/*` pattern — rather than `embedded` — is what allows the
// directory to contain only .gitkeep without breaking the build.
//
//go:embed embedded/*
var embedded embed.FS
