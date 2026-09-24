package assetref

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aue"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
)

// A map in an Auto-Pigeon account is an APMap, and a compiler reads a `.map`.
//
// # Why this exists (NEW_244D)
//
// A revision downloaded from the account is Auto-Pigeon's own editable format,
// `<name>.apmap`. Every Quake compiler this program runs reads `.map` text. So
// "choose a revision in My Maps and build it" — the path the README described —
// handed qbsp a JSON document, and the Q1 pipelines refused the input on its
// extension before that. Found by the operator's own acceptance run: build dm2
// and e1m6 from user1's account.
//
// # Why the extractor does it, and not this program
//
// The APMap → `.map` writer already exists twice in the workspace — the
// editor's kernel and the extractor's `convert --apmap-to-q1map` — and the
// extractor is the one this program is built to drive as a separate process
// (README "Extractor"). A third writer, in Go, here, would be a third opinion
// about plane points, texture projection and the source frame, and the first
// map where it disagreed would be a build that compiled something the editor
// never showed. So the conversion goes through the ONE runner this program has
// for the extractor, with its provenance — managed and verified, or the
// labelled developer override — recorded in the manifest beside the source.
//
// # What it refuses
//
// An APMap for a game this program has no direction for, and any conversion
// when no extractor is available: that is said by name, with what to do, and
// nothing is guessed. The `.apmap` itself is left untouched in the stage.

// ConvertedFormat names what a converted input became, for the manifest.
const ConvertedFormat = "map"

// ErrNoConverter reports an APMap input with no extractor to convert it.
var ErrNoConverter = errors.New(
	"this map is stored in Auto-Pigeon's own format (APMap), and turning it into a .map " +
		"a compiler reads needs map conversion, which is not installed on this machine")

// directions maps an APMap's `game` to the extractor's conversion direction.
var directions = map[string]string{
	"quake1": "--apmap-to-q1map",
	"quake2": "--apmap-to-q2map",
}

// ConvertAPMapInputs replaces every resolved input that is an `.apmap` with the
// `.map` the extractor writes for it, in a directory beside it in the stage,
// and records the conversion on that input's SourceRef.
func ConvertAPMapInputs(ctx context.Context, runner aue.Runner, resolved map[string]string,
	sources map[string]build.SourceRef,
) (map[string]string, map[string]build.SourceRef, error) {
	names := make([]string, 0, len(resolved))
	for name, path := range resolved {
		if strings.EqualFold(filepath.Ext(path), ".apmap") {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return resolved, sources, nil
	}
	sort.Strings(names)
	if !aue.Available(runner) {
		return nil, nil, fmt.Errorf("the input %q: %w", names[0], ErrNoConverter)
	}
	out := make(map[string]string, len(resolved))
	for name, path := range resolved {
		out[name] = path
	}
	for _, name := range names {
		source := resolved[name]
		game, err := apmapGame(source)
		if err != nil {
			return nil, nil, fmt.Errorf("the input %q: %w", name, err)
		}
		direction, ok := directions[game]
		if !ok {
			return nil, nil, fmt.Errorf("the input %q is an APMap for %q, which this build cannot turn into a .map", name, game)
		}
		dir := filepath.Join(filepath.Dir(source), "converted-"+name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, nil, fmt.Errorf("the input %q: creating %s: %w", name, dir, err)
		}
		if _, err := runner.Run(ctx, "convert", direction, "--input", source, "--output", dir, "--json"); err != nil {
			return nil, nil, fmt.Errorf("the input %q: converting it to a .map: %w", name, err)
		}
		target := filepath.Join(dir, strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))+".map")
		digest, err := fileDigest(target)
		if err != nil {
			return nil, nil, fmt.Errorf("the input %q: the conversion wrote no %s: %w", name, filepath.Base(target), err)
		}
		out[name] = target
		if sources != nil {
			if ref, ok := sources[name]; ok {
				provenance := runner.Provenance()
				ref.ConvertedTo = ConvertedFormat
				ref.ConvertedSHA256 = digest
				ref.ConvertedBy = provenance.Mode
				ref.ConverterVerified = provenance.Verified
				sources[name] = ref
			}
		}
	}
	return out, sources, nil
}

func apmapGame(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	var header struct {
		Game string `json:"game"`
	}
	if err := json.NewDecoder(io.LimitReader(file, 64<<20)).Decode(&header); err != nil {
		return "", fmt.Errorf("reading the APMap's game: %w", err)
	}
	if header.Game == "" {
		return "", errors.New("the APMap names no game, so there is no way to know which .map to write")
	}
	return header.Game, nil
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
