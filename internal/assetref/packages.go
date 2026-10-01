package assetref

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/q3packages"
)

// quake3Game is the APMap `game` whose documents may bind packages.
const quake3Game = "quake3"

// BoundPackages reads the package bindings of every Quake III APMap among a
// build's resolved inputs and gets those archives onto this machine, verified.
//
// It runs BEFORE the conversion, because the bindings are in the APMap and the
// `.map` the extractor writes has nowhere to carry them. A build with no
// Quake III APMap input, or whose map binds nothing, returns nil.
//
// account may be nil: signed out, or offline. A bound package already in the
// cache still builds; one that is not is refused by name.
func BoundPackages(ctx context.Context, account q3packages.Account, cache q3packages.Cache,
	resolved map[string]string,
) (*build.BoundPackages, error) {
	names := make([]string, 0, len(resolved))
	for name, path := range resolved {
		if strings.EqualFold(filepath.Ext(path), ".apmap") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var bound *build.BoundPackages
	for _, name := range names {
		header, err := apmapHeader(resolved[name])
		if err != nil || header.Game != quake3Game {
			// Not this function's refusal to make: the conversion reads the
			// same header next and says what is wrong with it.
			continue
		}
		ledger, err := q3packages.ReadLedger(resolved[name])
		if err != nil {
			return nil, fmt.Errorf("the input %q: %w", name, err)
		}
		if ledger == nil {
			continue
		}
		packages, err := q3packages.Resolve(ctx, account, cache, ledger)
		if err != nil {
			return nil, fmt.Errorf("the input %q: %w", name, err)
		}
		if bound == nil {
			bound = &build.BoundPackages{BaseRoot: ledger.BaseRoot, ModRoot: ledger.ModRoot}
		}
		bound.Packages = append(bound.Packages, packages...)
	}
	return bound, nil
}
