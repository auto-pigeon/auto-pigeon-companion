// Package launch fetches per-game launch configuration and starts games.
//
// config.go owns "what should be launched"; exec.go owns "start it". The split
// is what lets the execution half be tested against a hardcoded config while
// the AUB half is still a stub.
//
// TODO(andrea): AUB's collection name and schema for per-game launch configs is
// not confirmed. Everything in this file below Config is provisional and marked.
package launch

import (
	"context"
	"fmt"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-launcher/internal/aub"
)

// Config is one game's launch parameters.
//
// The field set is what exec.go needs to build a process, and no more. It is a
// guess at AUB's schema in name only — the shapes here (a path pattern with
// placeholders, an argv slice, a working directory) are what the execution side
// requires regardless of how AUB ends up spelling them.
type Config struct {
	// ID is AUB's record ID, empty for a locally synthesised config.
	ID string `json:"id,omitempty"`
	// Game is the user-facing name and the key callers select by.
	Game string `json:"game"`
	// ExecutablePattern is the path to the game binary, with placeholders
	// expanded by ResolveExecutable: {game_root}, {os}, {arch}, {exe}.
	ExecutablePattern string `json:"executable_pattern"`
	// Args is the argv tail passed to the game, placeholders expanded the same
	// way plus {map}.
	Args []string `json:"args,omitempty"`
	// WorkingDir is the process working directory; empty means the executable's
	// own directory, which is what most Quake engines expect.
	WorkingDir string `json:"working_dir,omitempty"`
}

// Provider supplies launch configs. The interface exists so the CLI and the web
// server depend on "somewhere to get configs from" rather than on AUB, which is
// what makes the stub below swappable for the real client without touching a
// caller.
type Provider interface {
	Configs(ctx context.Context) ([]Config, error)
}

// ExampleConfigs is the hardcoded stand-in returned until AUB's schema is
// confirmed.
//
// These are shaped like the real thing on purpose — a placeholder path, an argv
// with a {map} slot — so exec.go's path resolution and argument expansion are
// exercised against something representative rather than a trivial value.
func ExampleConfigs() []Config {
	return []Config{
		{
			Game:              "quake",
			ExecutablePattern: "{game_root}/quakespasm{exe}",
			Args:              []string{"-basedir", "{game_root}", "+map", "{map}"},
		},
		{
			Game:              "quake2",
			ExecutablePattern: "{game_root}/yquake2{exe}",
			Args:              []string{"+set", "basedir", "{game_root}", "+map", "{map}"},
		},
	}
}

// StaticProvider serves a fixed set of configs.
type StaticProvider struct{ Items []Config }

// Configs implements Provider.
func (p StaticProvider) Configs(context.Context) ([]Config, error) { return p.Items, nil }

// ExampleProvider is the stub Provider every caller currently uses.
func ExampleProvider() Provider { return StaticProvider{Items: ExampleConfigs()} }

// AUBCollection is the collection per-game launch configs are expected to live
// in.
//
// TODO(andrea): confirm. This name is a placeholder; nothing in AUB has been
// verified to serve it.
const AUBCollection = "launch_configs"

// AUBProvider reads launch configs from AUB.
//
// # Not yet wired up
//
// Configs deliberately returns an error rather than data. Writing a decoder
// against a guessed schema would produce code that looks finished, compiles,
// and is wrong in a way nobody notices until AUB's real schema lands and the
// field names silently decode to zero values. An explicit error is the honest
// state, and the request shape below records what is already known so the
// remaining work is filling in the record type.
type AUBProvider struct {
	Client     *aub.Client
	Collection string
}

// NewAUBProvider builds a provider against the given client.
func NewAUBProvider(client *aub.Client) *AUBProvider {
	return &AUBProvider{Client: client, Collection: AUBCollection}
}

// ErrSchemaUnconfirmed reports that the AUB-backed provider cannot run yet.
var ErrSchemaUnconfirmed = fmt.Errorf("launch: AUB's launch-config schema is not confirmed yet; use the example provider")

// Configs implements Provider.
func (p *AUBProvider) Configs(ctx context.Context) ([]Config, error) {
	if p.Client == nil {
		return nil, fmt.Errorf("launch: no AUB client")
	}
	if !p.Client.Authenticated() {
		return nil, fmt.Errorf("launch: not authenticated to AUB")
	}

	// The call this becomes, kept here so the TODO is one decode away from
	// done rather than one design away:
	//
	//	var page struct {
	//	    Items []Config `json:"items"`
	//	}
	//	query := url.Values{"perPage": {"200"}, "sort": {"game"}}
	//	if err := p.Client.ListRecords(ctx, p.Collection, query, &page); err != nil {
	//	    return nil, err
	//	}
	//	return page.Items, nil
	return nil, ErrSchemaUnconfirmed
}

// Find returns the config for a game, matched case-insensitively.
func Find(configs []Config, game string) (Config, error) {
	wanted := strings.TrimSpace(strings.ToLower(game))
	if wanted == "" {
		return Config{}, fmt.Errorf("launch: no game named")
	}
	for _, config := range configs {
		if strings.ToLower(config.Game) == wanted {
			return config, nil
		}
	}

	names := make([]string, 0, len(configs))
	for _, config := range configs {
		names = append(names, config.Game)
	}
	if len(names) == 0 {
		return Config{}, fmt.Errorf("launch: no launch config for %q (none are available)", game)
	}
	return Config{}, fmt.Errorf("launch: no launch config for %q (have: %s)", game, strings.Join(names, ", "))
}
