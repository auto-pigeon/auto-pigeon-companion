package cli

// Building from an AUB asset: `--input map=aub:map/<asset_id>@<revision>`.
//
// The parsing, cache resolution and manifest record all live in
// [github.com/auto-pigeon/auto-pigeon-companion/internal/assetref], because
// the GUI builds from assets too and a second copy would be a second chance for
// a `--input` and a form field to disagree about which revision a build read.
// What is left here is the CLI's own half: finding this invocation's cache and
// session, and saying out loud when it is going to build offline.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetref"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetsync"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
)

// AUBInputPrefix marks an input value as an asset reference rather than a path.
const AUBInputPrefix = assetref.Prefix

// AssetRef is a parsed `aub:<type>/<id>[@<revision>][#<file>]`.
type AssetRef = assetref.Ref

// IsAUBRef reports whether an input value is an asset reference.
func IsAUBRef(value string) bool { return assetref.Is(value) }

// ParseAssetRef reads `aub:map/abc123@rev456#e1m1.apmap`.
func ParseAssetRef(value string) (AssetRef, error) { return assetref.Parse(value) }

// resolveAUBInputs turns every `aub:` input into a local file and a SourceRef,
// and every APMap input — from the account or a local `.apmap` — into the
// `.map` a compiler reads.
func resolveAUBInputs(ctx context.Context, env *Env, inputs map[string]string, stage string,
) (map[string]string, map[string]build.SourceRef, error) {
	anyRef, anyLocalAPMap := false, false
	for _, value := range inputs {
		switch {
		case assetref.Is(value):
			anyRef = true
		case strings.EqualFold(filepath.Ext(value), ".apmap"):
			anyLocalAPMap = true
		}
	}
	if !anyRef && !anyLocalAPMap {
		return inputs, nil, nil
	}
	resolved := inputs
	var sources map[string]build.SourceRef
	if anyRef {
		store, err := openStore(env)
		if err != nil {
			return nil, nil, err
		}
		// A syncer is OPTIONAL: an offline Companion, or one whose session has
		// lapsed, still builds from what it has already verified.
		syncer, syncErr := openSyncerQuietly(ctx, env)
		if syncErr != nil {
			fmt.Fprintf(env.Stderr, "note: not fetching from AUB (%v); "+
				"pinned revisions already in the cache will still build\n", syncErr)
		}
		resolved, sources, err = assetref.ResolveAll(ctx, store, syncer, inputs, stage)
		if err != nil {
			return nil, nil, err
		}
	}
	resolved, err := stageLocalAPMaps(inputs, resolved, stage)
	if err != nil {
		return nil, nil, err
	}
	// An account map is an APMap; the compilers read `.map`. The extractor is
	// resolved lazily, so an input that needs no conversion fetches nothing.
	return assetref.ConvertAPMapInputs(ctx, extractorRunner(env, func(format string, args ...any) {
		fmt.Fprintf(env.Stderr, format+"\n", args...)
	}), resolved, sources)
}

// stageLocalAPMaps copies every local `.apmap` input into the build's stage.
//
// The conversion writes `converted-<name>/` beside the APMap it reads, and for a
// local input that would be the user's own directory: a build must not leave
// files next to somebody's source. An account map is already in the stage, and
// every other input is passed through unchanged. Before Q3_004 a local `.apmap`
// reached the compiler unconverted, for every game.
func stageLocalAPMaps(inputs, resolved map[string]string, stage string) (map[string]string, error) {
	out := make(map[string]string, len(resolved))
	for name, path := range resolved {
		out[name] = path
		original := inputs[name]
		if assetref.Is(original) || !strings.EqualFold(filepath.Ext(path), ".apmap") {
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

// openSyncerQuietly is openSyncer without treating an absent session as fatal.
func openSyncerQuietly(ctx context.Context, env *Env) (*assetsync.Syncer, error) {
	syncer, _, err := openSyncer(ctx, env)
	if err != nil {
		return nil, err
	}

	return syncer, nil
}
