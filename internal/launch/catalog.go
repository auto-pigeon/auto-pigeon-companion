package launch

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// Launch configs, as a profile catalog the executor can read.
//
// This is the join between "what AUB says about a game" and "what the executor
// runs". Everything it produces goes through [EngineProfile], so the documents
// here are the same shape, validated by the same rules, as a profile somebody
// publishes.

// Catalog presents each launch config as a generated engine profile.
type Catalog struct {
	Provider Provider
	// Context bounds the provider call. Nil means context.Background: the stub
	// provider is in memory, and the AUB-backed one will want a real one.
	Context context.Context
}

// NewCatalog wraps a provider.
func NewCatalog(provider Provider) Catalog { return Catalog{Provider: provider} }

func (c Catalog) ctx() context.Context {
	if c.Context != nil {
		return c.Context
	}
	return context.Background()
}

// List implements [job.Catalog].
func (c Catalog) List() ([]job.CatalogEntry, error) {
	if c.Provider == nil {
		return nil, nil
	}
	configs, err := c.Provider.Configs(c.ctx())
	if err != nil {
		return nil, err
	}
	out := make([]job.CatalogEntry, 0, len(configs))
	for _, config := range configs {
		document, err := EngineProfile(config)
		if err != nil {
			// A config this build cannot describe is skipped rather than
			// fatal: one unplaceable game must not make every other game
			// unlaunchable. `companion launch <that game>` still reports it,
			// because Lookup returns the same error by name.
			continue
		}
		digest, err := profile.Digest(document)
		if err != nil {
			return nil, err
		}
		out = append(out, job.CatalogEntry{
			Profile: document,
			// Trusted the way an embedded document is, and for the same
			// reason: this build generated it, from configuration the user
			// already had. No third party's bytes are in it.
			Trust:  profile.TrustBuiltin,
			Digest: digest,
			Source: "generated from the launch config for " + config.Game,
		})
	}
	return out, nil
}

// Lookup implements [job.Catalog].
func (c Catalog) Lookup(id string) (job.CatalogEntry, error) {
	entries, err := c.List()
	if err != nil {
		return job.CatalogEntry{}, err
	}
	for _, entry := range entries {
		if entry.Profile.Metadata().ID == id {
			return entry, nil
		}
	}
	return job.CatalogEntry{}, fmt.Errorf("%w: %q", job.ErrNoProfile, id)
}

// JobRequest turns "launch this game on this map" into a job.
//
// The machine's half — where the engine actually is — arrives as request
// overrides rather than in the document, which is the same split every other
// profile uses: the portable part says what to run, the local part says where
// it is.
func JobRequest(config Config, gameRoot, mapName string, extraArgs []string) (job.Request, error) {
	plan, err := Resolve(Request{Config: config, GameRoot: gameRoot, Map: mapName, ExtraArgs: extraArgs})
	if err != nil {
		return job.Request{}, err
	}
	root := gameRoot
	if root == "" {
		root = filepath.Dir(plan.Executable)
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return job.Request{}, fmt.Errorf("launch: resolving %s: %w", root, err)
	}
	absoluteExecutable, err := filepath.Abs(plan.Executable)
	if err != nil {
		return job.Request{}, fmt.Errorf("launch: resolving %s: %w", plan.Executable, err)
	}
	request := job.Request{
		ProfileID:   GeneratedFor(config.Game),
		ActionID:    profile.ActionPlayMap,
		Executables: map[string]string{"engine": absoluteExecutable},
		Roots:       map[string]string{profile.RootGame: absoluteRoot},
		Label:       config.Game,
	}
	if mapName != "" {
		request.Runtime = map[string]string{profile.RuntimeMapName: mapName}
	}
	return request, nil
}
