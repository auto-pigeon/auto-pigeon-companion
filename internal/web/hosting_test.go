package web

import (
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// The port a hosted game is listed at is a suggestion the page shows: the
// profile's own option when it declares one, the game's default otherwise.
func TestSuggestedPortPrefersTheProfileThenTheGame(t *testing.T) {
	declared := &profile.EngineProfile{
		GameProfile: profile.GameProfileRef{EngineFamily: "quake3"},
		Actions: []profile.Action{{ID: "host_dedicated",
			Options: []profile.OptionSpec{{Name: "port", Default: "27961"}}}},
	}
	if port, source := suggestedPort(declared, "host_dedicated"); port != 27961 || source != "profile" {
		t.Errorf("declared: got %d %s", port, source)
	}
	if port, source := suggestedPort(declared, "host_listen"); port != 27960 || source != "game_default" {
		t.Errorf("undeclared action: got %d %s", port, source)
	}
	unknown := &profile.EngineProfile{GameProfile: profile.GameProfileRef{EngineFamily: "doom"}}
	if port, _ := suggestedPort(unknown, "host_listen"); port != 0 {
		t.Errorf("a game with no known default got port %d; nothing may be guessed", port)
	}
}

// Only a hosted game is listed, and only with an address and a port.
func TestAListingNeedsAHostedGameAndAnAddress(t *testing.T) {
	server, _ := newTestServer(t, nil)
	request := playrun.Request{AssetID: "m", RevisionNumber: 1, EngineProfileID: "x", EngineActionID: "play_map"}
	if _, err := server.listingRegistration(request, &playListingBody{Title: "t", EndpointHost: "h", EndpointPort: 1}); err == nil {
		t.Error("a play_map run was accepted as a listed game")
	}
	if _, err := server.listingRegistration(request, nil); err == nil {
		t.Error("a registration without a listing was accepted")
	}
}
