package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
)

// A map that came from Auto-Pigeon reaches a compiler only as a fresh copy
// written from a parsed map — never as the bytes that were downloaded.
//
// # Why
//
// A map revision from AUB is written by whoever may save that map, and the
// Companion compiles it on somebody else's machine, with their files; a hosted
// game then uploads the BSP, which other readers of the map can download. A
// map's text names files the compiler opens (worldspawn `wad`, `_external_map`,
// Quake III `model`/`_remap`, a face's texture name) and sets compiler options
// (ericw light takes every worldspawn key as a setting, `_radlights` a file).
// `NEW_348` answered with a byte scanner over the original text; a Quake fence
// texture `{fence` moved its brace counter and hid every later entity, and it
// modelled ericw's `//` comments but not its `;` ones (`NEW_349` N3, F9, F2 and
// the Windows texture-token lead). A scanner that guesses at a grammar the
// compiler reads differently is the bug.
//
// # What happens now (`NEW_434`)
//
// An input that has a SourceRef and is map text is never copied. The bundled
// converter's `canonical-map` (cmd/mapio/canonical.go, where the rule is
// written down in full) parses it with go/mapio's Quake III reader — Q3Map2's
// tokenizer, which reads every Quake 1, 2 and 3 map the pipelines here compile —
// holds every token to a rule ericw and Q3Map2 read alike, refuses names and
// keys that reach outside the build, and writes a new file the Companion named
// in this build's own input folder; that file is what every step compiles.
//
// It runs in the converter because only that program links go/mapio
// (docs/agent/aucom.extractor-acquisition.md): the Companion links no external
// module. With no converter there is no check, and a map from Auto-Pigeon is
// refused rather than compiled as it came.
//
// A file the user pointed at on their own disk has no SourceRef and is staged
// exactly as it is — their own map naming their own files is not this case. A
// compiler's arguments come from its profile and stage as an argument vector,
// never from the map.

// CanonicalMapFunc writes the canonical copy of the map at source to
// destination, a file that must not exist yet, and returns the worldspawn WAD
// entries it reduced to their file names. converter.CanonicalMap is the one
// production implementation.
type CanonicalMapFunc func(ctx context.Context, source, destination string) ([]string, error)

// errNoMapCheck is the refusal when nothing can check a map from Auto-Pigeon.
var errNoMapCheck = errors.New("this map came from Auto-Pigeon, and it is compiled only as the copy the map " +
	"converter checks and writes; no converter is available to this build, so nothing was compiled. " +
	"Reinstall the Companion, or see `companion converter status`")

// isMapInput reports whether a pipeline input is map text: its role says so
// (`q1.map.source`, …), or its file is a `.map`. A map-role input from
// Auto-Pigeon that is not map text is refused by the parse, not passed on.
func isMapInput(role, source string) bool {
	return strings.HasSuffix(role, ".map.source") || strings.EqualFold(filepath.Ext(source), ".map")
}

// checkAUBMapDestination refuses a staged name that is not one plain file name
// in a folder of this build's own: the name comes from the downloaded file's,
// and a link in the way would send the copy somewhere else.
func checkAUBMapDestination(inputRoot, destination string) error {
	name := filepath.Base(destination)
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") || len(name) > 255 ||
		strings.ContainsAny(name, `/\:`) || strings.IndexFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("the map's file name is not one plain name, so it is not staged")
	}
	root, err := filepath.EvalSymlinks(inputRoot)
	if err != nil {
		return err
	}
	folder, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return err
	}
	if relative, err := filepath.Rel(root, folder); err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("the map's staging folder is not inside this build")
	}
	if _, err := os.Lstat(destination); err == nil {
		return errors.New("something is already where the map's copy goes, so it is not staged")
	}
	return nil
}

// stageAUBMap has canonical write the copy of the map at source to
// destination, and checks what it left there.
func stageAUBMap(ctx context.Context, canonical CanonicalMapFunc, inputRoot, source, destination string) ([]string, error) {
	if canonical == nil {
		return nil, failure.As(failure.ConverterUnavailable, errNoMapCheck)
	}
	if err := checkAUBMapDestination(inputRoot, destination); err != nil {
		return nil, err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", source)
	}
	reduced, err := canonical(ctx, source, destination)
	if err != nil {
		return nil, err
	}
	staged, err := os.Lstat(destination)
	if err != nil {
		return nil, fmt.Errorf("the map converter reported a copy it did not write: %w", err)
	}
	if !staged.Mode().IsRegular() {
		return nil, errors.New("the map converter's copy is not a regular file")
	}
	return reduced, nil
}
