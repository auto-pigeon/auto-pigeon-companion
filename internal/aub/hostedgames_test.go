package aub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// hostedGameServer answers the lifecycle routes and records what it was sent, so
// the request shape is asserted rather than assumed.
func hostedGameServer(t *testing.T) (*Client, *[]*http.Request, *[]map[string]any) {
	t.Helper()

	var seen []*http.Request
	var bodies []map[string]any

	record := func(r *http.Request) {
		seen = append(seen, r)
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
	}

	game := map[string]any{
		"id": "gme1", "title": "Friday deathmatch", "state": "live",
		"lifecycle_reason": "running", "map_id": "map1", "map_revision": 7,
		"game_family": "quake1", "engine_runtime": "quakespasm", "mode": "listen",
		"visibility": "public", "reachability": "unverified", "endpoint_scope": "public",
		"endpoint": "203.0.113.4:26000", "players_source": "host",
		"heartbeat_interval_seconds": 30,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/hosted-games/vocabulary", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		json.NewEncoder(w).Encode(map[string]any{
			"schema_version": HostedGameSchema, "modes": []string{"listen", "dedicated"},
			"heartbeat_interval_seconds": 30, "lease_ttl_seconds": 120,
			"join_link_scheme": "autopigeon", "occupancy_note": "counted by nobody",
		})
	})
	mux.HandleFunc("POST /api/hosted-games/preview", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		json.NewEncoder(w).Encode(map[string]any{
			"schema_version": HostedGameSchema, "visibility": "public",
			"audience": "everybody signed in", "endpoint_published": true,
			"endpoint": "203.0.113.4:26000", "endpoint_scope": "public",
			"reachability": "unverified",
			"exposed_fields": []map[string]any{
				{"field": "title", "value": "Friday deathmatch", "seen_by": "everybody signed in"},
			},
		})
	})
	mux.HandleFunc("POST /api/hosted-games", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"schema_version": HostedGameSchema, "reclaimed": false, "game": game,
		})
	})
	mux.HandleFunc("POST /api/hosted-games/gme1/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		json.NewEncoder(w).Encode(map[string]any{
			"schema_version": HostedGameSchema, "game": game, "next_heartbeat_in_seconds": 30,
		})
	})
	mux.HandleFunc("POST /api/hosted-games/gme1/stop", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		ended := map[string]any{}
		for key, value := range game {
			ended[key] = value
		}
		ended["state"] = "offline"
		ended["lifecycle_reason"] = "host_stopped"
		json.NewEncoder(w).Encode(map[string]any{"schema_version": HostedGameSchema, "game": ended})
	})
	mux.HandleFunc("POST /api/hosted-games/gme1/join", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"schema_version": HostedGameSchema,
			"ticket": map[string]any{
				"ticket_id": "tkt1", "link": "autopigeon://join/tkt1",
				"game_id": "gme1", "expires_in_seconds": 120,
			},
		})
	})
	mux.HandleFunc("POST /api/hosted-games/join-tickets/tkt1/resolve",
		func(w http.ResponseWriter, r *http.Request) {
			record(r)
			json.NewEncoder(w).Encode(map[string]any{
				"schema_version": HostedGameSchema, "game_id": "gme1", "title": "Friday deathmatch",
				"endpoint": "203.0.113.4:26000", "endpoint_host": "203.0.113.4",
				"endpoint_port": 26000, "engine_runtime": "quakespasm", "engine_action": "join_server",
				"map_id": "map1", "map_revision": 7,
				"assets": map[string]any{"readable": true, "access_path": "public"},
			})
		})
	mux.HandleFunc("GET /api/hosted-games", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		json.NewEncoder(w).Encode(map[string]any{
			"schema_version": HostedGameSchema, "games": []any{game},
		})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	client.SetToken("token-1")

	return client, &seen, &bodies
}

func TestTheHostedGameClientSendsTheAccountsTokenOnEveryCall(t *testing.T) {
	client, seen, _ := hostedGameServer(t)
	ctx := context.Background()

	if _, err := client.HostedGameVocabularyDoc(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.HostedGames(ctx, HostedGameQuery{}); err != nil {
		t.Fatal(err)
	}
	for _, request := range *seen {
		if request.Header.Get("Authorization") != "token-1" {
			t.Fatalf("%s %s carried no session", request.Method, request.URL.Path)
		}
	}
}

// The client refuses to send an unconfirmed registration, so a caller who skipped
// the review gets a message naming the review.
func TestAnUnconfirmedRegistrationNeverLeavesThisProgram(t *testing.T) {
	client, seen, _ := hostedGameServer(t)

	_, err := client.RegisterHostedGame(context.Background(), HostedGameRegistration{Title: "x"})
	if err == nil {
		t.Fatal("an unconfirmed registration was sent")
	}
	if !strings.Contains(err.Error(), "seen what the listing will say") {
		t.Fatalf("the refusal does not name the review: %v", err)
	}
	if len(*seen) != 0 {
		t.Fatalf("%d requests were made", len(*seen))
	}
}

// A preview is the review, so it never demands the confirmation it informs.
func TestAPreviewForcesTheConfirmationTrueOnTheWayOut(t *testing.T) {
	client, _, bodies := hostedGameServer(t)

	if _, err := client.PreviewHostedGame(context.Background(),
		HostedGameRegistration{Title: "x", ConfirmExposure: false}); err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 1 || (*bodies)[0]["confirm_exposure"] != true {
		t.Fatalf("the preview sent %+v", *bodies)
	}
}

// A beat that says nothing about players sends no player members at all, which is
// what makes "I have nothing to report" different from "zero players".
func TestABeatWithNothingToReportSendsNoCounts(t *testing.T) {
	client, _, bodies := hostedGameServer(t)

	if _, err := client.HeartbeatHostedGame(context.Background(), "gme1", HeartbeatBody{}); err != nil {
		t.Fatal(err)
	}
	body := (*bodies)[0]
	for _, key := range []string{"players_current", "players_max", "players_observable"} {
		if _, present := body[key]; present {
			t.Fatalf("an empty beat carried %q, which would overwrite the listing's counts with zeros", key)
		}
	}

	zero, max := 0, 8
	observable := true
	if _, err := client.HeartbeatHostedGame(context.Background(), "gme1", HeartbeatBody{
		PlayersCurrent: &zero, PlayersMax: &max, PlayersObservable: &observable,
	}); err != nil {
		t.Fatal(err)
	}
	body = (*bodies)[1]
	if body["players_current"] != float64(0) || body["players_max"] != float64(8) {
		t.Fatalf("an explicit zero did not travel: %+v", body)
	}
}

func TestTheWholeLifecycleRoundTrips(t *testing.T) {
	client, _, _ := hostedGameServer(t)
	ctx := context.Background()

	result, err := client.RegisterHostedGame(ctx, HostedGameRegistration{
		Title: "Friday deathmatch", MapID: "map1", GameFamily: "quake1",
		EngineRuntime: "quakespasm", Mode: ModeListen,
		EndpointHost: "203.0.113.4", EndpointPort: 26000,
		Visibility: GamePublic, ConfirmExposure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Game.Live() || result.Game.HeartbeatInterval().Seconds() != 30 {
		t.Fatalf("game = %+v", result.Game)
	}

	ticket, err := client.MintJoinLink(ctx, "gme1")
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Link != "autopigeon://join/tkt1" {
		t.Fatalf("link = %q", ticket.Link)
	}
	// The link carries the id and nothing else: no token, no address, no map.
	for _, secret := range []string{"token-1", "203.0.113.4", "map1"} {
		if strings.Contains(ticket.Link, secret) {
			t.Fatalf("the link carries %q", secret)
		}
	}

	id, err := ParseJoinLink(ticket.Link)
	if err != nil {
		t.Fatal(err)
	}
	join, err := client.ResolveJoinLink(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if join.Action != "join_server" || !join.Assets.Readable {
		t.Fatalf("join = %+v", join)
	}

	stopped, err := client.StopHostedGame(ctx, "gme1", ReasonHostStopped)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Game.Live() || stopped.Game.Reason != "host_stopped" {
		t.Fatalf("stopped = %+v", stopped.Game)
	}
}

// A deployment serving another version of this contract is refused rather than
// parsed optimistically.
func TestAnUnknownSchemaVersionIsRefused(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"schema_version": "aub-hosted-game-lifecycle/9.9"})
	}))
	t.Cleanup(server.Close)
	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.HostedGameVocabularyDoc(context.Background()); err == nil {
		t.Fatal("a future contract was accepted")
	}
}

// A heartbeat cadence is read from the answer, never chosen here.
func TestTheHeartbeatCadenceComesFromTheServer(t *testing.T) {
	game := HostedGame{HeartbeatSecs: 45}
	if game.HeartbeatInterval().Seconds() != 45 {
		t.Fatalf("interval = %v", game.HeartbeatInterval())
	}
	// A server that said nothing gets the documented default rather than zero,
	// because a zero interval is a beat loop that never sleeps.
	silent := HostedGame{}
	if silent.HeartbeatInterval().Seconds() != 30 {
		t.Fatalf("interval = %v", silent.HeartbeatInterval())
	}
}
