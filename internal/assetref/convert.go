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
	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
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
//
// `quake3` is the extractor's `apmap-to-q3map` (Q3_004): Quake III `.map` text for Q3Map2, with
// the editor's own bytes, written beside a `<stem>.q3map-manifest.json` the extractor puts in the
// same `converted-<name>/` directory. It is only ever that direction — a Quake III document sent to
// the Quake 1 or Quake 2 writer would lose its patches, and the extractor refuses that by game. An
// extractor that predates the direction refuses the flag, and that refusal is the error reported.
var directions = map[string]string{
	"quake1": "--apmap-to-q1map",
	"quake2": "--apmap-to-q2map",
	"quake3": "--apmap-to-q3map",
}

// ConvertAPMapInputs replaces every resolved input that is an `.apmap` with the
// `.map` the extractor writes for it, in a directory beside it in the stage,
// and records the conversion on that input's SourceRef.
func ConvertAPMapInputs(ctx context.Context, runner aue.Runner, resolved map[string]string,
	sources map[string]build.SourceRef,
) (map[string]string, map[string]build.SourceRef, error) {
	out, sources, _, err := ConvertAPMapInputsRecorded(ctx, runner, resolved, sources)
	return out, sources, err
}

// ConvertAPMapInputsRecorded is [ConvertAPMapInputs] that also returns, for
// every input it converted, the record of what it was converted FROM: the
// APMap's digest and its own document identity, and the extractor's conversion
// manifest when the direction writes one (Q3_010).
//
// The record is keyed by input name and exists whether or not the input has a
// SourceRef — a local `.apmap` has none, and its conversion is still a fact
// about the file a build compiled.
func ConvertAPMapInputsRecorded(ctx context.Context, runner aue.Runner, resolved map[string]string,
	sources map[string]build.SourceRef,
) (map[string]string, map[string]build.SourceRef, map[string]build.Conversion, error) {
	out, sources, conversions, err := convertAPMapInputs(ctx, runner, resolved, sources)
	if err != nil {
		return nil, nil, nil, err
	}
	return out, sources, conversions, nil
}

func convertAPMapInputs(ctx context.Context, runner aue.Runner, resolved map[string]string,
	sources map[string]build.SourceRef,
) (map[string]string, map[string]build.SourceRef, map[string]build.Conversion, error) {
	names := make([]string, 0, len(resolved))
	for name, path := range resolved {
		if strings.EqualFold(filepath.Ext(path), ".apmap") {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return resolved, sources, nil, nil
	}
	sort.Strings(names)
	if !aue.Available(runner) {
		return nil, nil, nil, failure.As(failure.ConverterUnavailable,
			fmt.Errorf("the input %q: %w", names[0], ErrNoConverter))
	}
	conversions := make(map[string]build.Conversion, len(names))
	out := make(map[string]string, len(resolved))
	for name, path := range resolved {
		out[name] = path
	}
	for _, name := range names {
		source := resolved[name]
		header, err := apmapHeader(source)
		if err != nil {
			return nil, nil, nil, failure.As(failure.ConversionRefused, fmt.Errorf("the input %q: %w", name, err))
		}
		direction, ok := directions[header.Game]
		if !ok {
			return nil, nil, nil, failure.As(failure.ConversionRefused, fmt.Errorf(
				"the input %q is an APMap for %q, which this build cannot turn into a .map", name, header.Game))
		}
		sourceDigest, sourceBytes, err := fileDigestSize(source)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("the input %q: %w", name, err)
		}
		dir := filepath.Join(filepath.Dir(source), "converted-"+name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, nil, nil, fmt.Errorf("the input %q: creating %s: %w", name, dir, err)
		}
		if _, err := runner.Run(ctx, "convert", direction, "--input", source, "--output", dir, "--json"); err != nil {
			return nil, nil, nil, failure.As(failure.ConversionRefused, conversionError(name, err))
		}
		stem := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
		target := filepath.Join(dir, stem+".map")
		digest, err := fileDigest(target)
		if err != nil {
			return nil, nil, nil, failure.As(failure.ConversionRefused,
				fmt.Errorf("the input %q: the conversion wrote no %s: %w", name, filepath.Base(target), err))
		}
		out[name] = target
		provenance := runner.Provenance()
		conversion := build.Conversion{
			From: "apmap", To: ConvertedFormat, Direction: strings.TrimPrefix(direction, "--"),
			SourceName: filepath.Base(source), SourceSHA256: sourceDigest, SourceBytes: sourceBytes,
			DocumentID: header.DocumentID, DocumentRevision: header.Revision,
			APMapVersion: header.Version, Game: header.Game,
			ConvertedBy: provenance.Mode, ConverterVerified: provenance.Verified,
		}
		recorded, err := conversionManifest(filepath.Join(dir, stem+conversionManifestSuffix), sourceDigest, digest)
		if err != nil {
			return nil, nil, nil, failure.As(failure.ConversionRefused, fmt.Errorf("the input %q: %w", name, err))
		}
		conversion.Manifest = recorded
		conversions[name] = conversion
		if sources != nil {
			if ref, ok := sources[name]; ok {
				ref.ConvertedTo = ConvertedFormat
				ref.ConvertedSHA256 = digest
				ref.ConvertedBy = provenance.Mode
				ref.ConverterVerified = provenance.Verified
				sources[name] = ref
			}
		}
	}
	return out, sources, conversions, nil
}

// conversionError says why the extractor would not write a `.map`, with the
// extractor's own reason FIRST.
//
// The reason is in the terminal record on the extractor's last stderr line —
// `apmap-to-q3map refused [q3map_shader_unsafe]: fac_… : shader "…" is not a
// safe Quake III shader path` — after an exit code and a version banner and
// before an incident envelope. Printed whole, it was all there and nobody
// could find it (`Q3_007`: "was not shown in the Companion page"). So the
// sentence a person reads is the extractor's, and everything it printed stays
// underneath, because that is still the record.
func conversionError(name string, err error) error {
	if terminal, ok := aue.ReadTerminal(err.Error()); ok && terminal.Sentence() != "" {
		return fmt.Errorf("the input %q: the extractor would not convert it to a .map: %s\n%w",
			name, terminal.Sentence(), err)
	}
	return fmt.Errorf("the input %q: converting it to a .map: %w", name, err)
}

// conversionManifestSuffix is what the extractor's Quake III direction names
// the manifest it writes beside the `.map`.
const conversionManifestSuffix = ".q3map-manifest.json"

// conversionManifest reads the extractor's conversion manifest, when the
// direction wrote one, and checks that it is about THIS conversion.
//
// The manifest states the digest of the APMap it read and of the `.map` it
// wrote. Both are compared with the files this program holds: a manifest left
// over from another conversion in the same directory, describing other bytes,
// would be a record of the wrong map, and that is refused rather than kept.
// A direction that writes no manifest (Quake 1, Quake II) returns nil.
func conversionManifest(path, sourceDigest, outputDigest string) (*build.ConversionManifest, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the conversion manifest: %w", err)
	}
	var document struct {
		Format string `json:"format"`
		Source struct {
			SHA256 string `json:"sha256"`
		} `json:"source"`
		Output struct {
			SHA256 string `json:"sha256"`
		} `json:"output"`
		Shaders  []json.RawMessage `json:"shaders"`
		Models   []json.RawMessage `json:"models"`
		Warnings []json.RawMessage `json:"warnings"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("the conversion manifest %s is not readable: %w", filepath.Base(path), err)
	}
	if got := "sha256:" + document.Source.SHA256; got != sourceDigest {
		return nil, fmt.Errorf("the conversion manifest %s describes an APMap with digest %s, and the one converted is %s",
			filepath.Base(path), got, sourceDigest)
	}
	if got := "sha256:" + document.Output.SHA256; got != outputDigest {
		return nil, fmt.Errorf("the conversion manifest %s describes a .map with digest %s, and the one written is %s",
			filepath.Base(path), got, outputDigest)
	}
	sum := sha256.Sum256(raw)
	return &build.ConversionManifest{
		Name: filepath.Base(path), Path: path, SHA256: "sha256:" + hex.EncodeToString(sum[:]),
		Format:   document.Format,
		Warnings: len(document.Warnings), Shaders: len(document.Shaders), Models: len(document.Models),
	}, nil
}

// StageLocalAPMaps copies every local `.apmap` input into the build's stage.
//
// The conversion writes `converted-<name>/` beside the APMap it reads, and for a
// local input that would be the user's own directory: a build must not leave
// files next to somebody's source. An account map is already in the stage, and
// every other input is passed through unchanged. Before Q3_004 a local `.apmap`
// reached the compiler unconverted, for every game; before Q3_010 the Build
// page still sent one there, because only the terminal staged them.
func StageLocalAPMaps(inputs, resolved map[string]string, stage string) (map[string]string, error) {
	out := make(map[string]string, len(resolved))
	for name, path := range resolved {
		out[name] = path
		if Is(inputs[name]) || !strings.EqualFold(filepath.Ext(path), ".apmap") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("the input %q: %w", name, err)
		}
		dir := filepath.Join(stage, "local-"+name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("the input %q: %w", name, err)
		}
		staged := filepath.Join(dir, filepath.Base(path))
		if err := os.WriteFile(staged, data, 0o600); err != nil {
			return nil, fmt.Errorf("the input %q: %w", name, err)
		}
		out[name] = staged
	}
	return out, nil
}

// HasLocalAPMap reports whether any input is a local `.apmap` path.
func HasLocalAPMap(inputs map[string]string) bool {
	for _, value := range inputs {
		if !Is(value) && strings.EqualFold(filepath.Ext(value), ".apmap") {
			return true
		}
	}
	return false
}

// apmapDocument is what this program reads out of an APMap's header: which
// game it is for — that chooses the direction — and the document's own
// identity, which a build records.
type apmapDocument struct {
	Game       string `json:"game"`
	DocumentID string `json:"document_id"`
	Revision   int    `json:"revision"`
	Version    string `json:"apmap_version"`
}

func apmapHeader(path string) (apmapDocument, error) {
	var header apmapDocument
	file, err := os.Open(path)
	if err != nil {
		return header, err
	}
	defer file.Close()
	if err := json.NewDecoder(io.LimitReader(file, 64<<20)).Decode(&header); err != nil {
		return header, fmt.Errorf("reading the APMap's game: %w", err)
	}
	if header.Game == "" {
		return header, errors.New("the APMap names no game, so there is no way to know which .map to write")
	}
	return header, nil
}

func fileDigestSize(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), size, nil
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
