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

	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetref"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetsync"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3packages"
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
) (map[string]string, map[string]build.SourceRef, map[string]build.Conversion, *build.BoundPackages, error) {
	anyRef := false
	for _, value := range inputs {
		if assetref.Is(value) {
			anyRef = true
		}
	}
	if !anyRef && !assetref.HasLocalAPMap(inputs) {
		return inputs, nil, nil, nil, nil
	}
	resolved := inputs
	var sources map[string]build.SourceRef
	if anyRef {
		store, err := openStore(env)
		if err != nil {
			return nil, nil, nil, nil, err
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
			return nil, nil, nil, nil, err
		}
	}
	resolved, err := assetref.StageLocalAPMaps(inputs, resolved, stage)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// An account map is an APMap; the compilers read `.map`. The extractor is
	// resolved lazily, so an input that needs no conversion fetches nothing.
	// A saved Quake III map names the packages it is built with, by digest. They
	// are read out of the APMap now — the `.map` has nowhere to carry them — and
	// fetched through the same cache and the same session the map came through.
	packages, err := boundPackages(ctx, env, resolved)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	converted, sources, conversions, err := assetref.ConvertAPMapInputsRecorded(ctx, extractorRunner(env, func(format string, args ...any) {
		fmt.Fprintf(env.Stderr, format+"\n", args...)
	}), resolved, sources)
	return converted, sources, conversions, packages, err
}

// boundPackages resolves a build's bound packages with whatever session this
// invocation has. No session is not an error here: a package already in the
// cache builds offline, and one that is not is refused by name, by the resolver.
func boundPackages(ctx context.Context, env *Env, resolved map[string]string) (*build.BoundPackages, error) {
	store, err := openStore(env)
	if err != nil {
		return nil, err
	}
	var account q3packages.Account
	if settings, err := loadSettings(env); err == nil && settings.Session.Valid() {
		if client, err := newClient(settings); err == nil && client.Authenticated() {
			account = client
		}
	}
	return assetref.BoundPackages(ctx, account, store, resolved)
}

// openSyncerQuietly is openSyncer without treating an absent session as fatal.
func openSyncerQuietly(ctx context.Context, env *Env) (*assetsync.Syncer, error) {
	syncer, _, err := openSyncer(ctx, env)
	if err != nil {
		return nil, err
	}

	return syncer, nil
}
