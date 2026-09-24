package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/enginefixture"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/joinintent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// The Games area through the real server, against a fake AUB that speaks the
// hosted-game contract: listing, detail, join content, ticket mint and redeem,
// game profiles and the profile catalogue. The joiner, the readiness model, the
// stager, the approval service and the job service are the shipped ones.

type gameFixture struct {
	t  *testing.T
	mu sync.Mutex

	game     map[string]any
	pkg      aub.JoinPackage
	bodies   map[string][]byte
	minted   int
	redeemed []string
	tickets  map[string]bool

	listing  *publishedFixture
	versions int
}

type publishedFixture struct {
	id       string
	document []byte
	digest   string
}

func newGameFixture(t *testing.T) *gameFixture {
	t.Helper()
	g := &gameFixture{t: t, tickets: map[string]bool{}, bodies: map[string][]byte{
		"maps/chapel.bsp": []byte("BSP29" + strings.Repeat("\x07", 4096)),
	}}
	g.pkg = aub.JoinPackage{SchemaVersion: aub.JoinContentSchema, MapID: "map1", MapRevision: 3, GameFamily: "quake1"}
	for destination, body := range g.bodies {
		sum := sha256.Sum256(body)
		g.pkg.Files = append(g.pkg.Files, aub.JoinContentFile{Role: "bsp", Destination: destination,
			Bytes: int64(len(body)), SHA256: hex.EncodeToString(sum[:])})
	}
	g.pkg.PackageSHA256 = g.pkg.Manifest().Digest()
	g.pkg.TotalBytes, g.pkg.FileCount = 4101, 1
	g.game = map[string]any{
		"id": "game1", "title": "Chapel night", "host_nickname": "Vera", "owned_by_me": false,
		"access_path": "public", "visibility": "public", "state": "live", "lifecycle_reason": "running",
		"map_id": "map1", "map_name": "Sunken Chapel", "map_revision": 3, "map_public": true,
		"package_sha256": g.pkg.PackageSHA256, "game_family": "quake1", "game_slug": "quake1",
		"engine_runtime": "aucom-fixture", "mode": "listen", "endpoint": "203.0.113.4:26000",
		"endpoint_scope": "public", "reachability": "unverified", "players_current": 1, "players_max": 8,
		"players_observable": true, "players_source": "host", "stale_for_ms": 2000, "joinable": true,
		"join_content": map[string]any{"state": "required", "package_sha256": g.pkg.PackageSHA256,
			"total_bytes": 4101, "file_count": 1, "source": "host", "readable": true},
	}

	return g
}

func (g *gameFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	write := func(status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	if r.Header.Get("Authorization") == "" {
		write(http.StatusUnauthorized, map[string]any{"message": "no session"})
		return
	}
	path := r.URL.Path
	schema := aub.HostedGameSchema
	switch {
	case strings.HasPrefix(path, "/api/game-profiles"):
		write(http.StatusOK, map[string]any{"items": []map[string]any{{"slug": "quake1", "name": "Quake",
			"engine_family": "quake1", "system_owned": true}}})
	case g.listing != nil && path == aub.ProfileCatalogPrefix+"/"+g.listing.id:
		write(http.StatusOK, map[string]any{"schema_version": aub.ProfileCatalogSchema, "profile": map[string]any{
			"id": g.listing.id, "profile_id": "aucom.fixture.published-engine", "kind": "engine",
			"name": "Published fixture engine", "trust": "verified", "latest_version": "1.0.0"}})
	case g.listing != nil && path == aub.ProfileCatalogPrefix+"/"+g.listing.id+"/versions/1.0.0":
		g.versions++
		write(http.StatusOK, map[string]any{"schema_version": aub.ProfileCatalogSchema,
			"profile_id": "aucom.fixture.published-engine", "kind": "engine", "trust": "verified",
			"version": map[string]any{"version": "1.0.0", "digest": g.listing.digest, "kind": "engine",
				"document": string(g.listing.document)}})
	case path == aub.HostedGamePrefix:
		write(http.StatusOK, map[string]any{"schema_version": schema, "games": []any{g.game},
			"server_time": time.Now().UTC()})
	case path == aub.HostedGamePrefix+"/game1":
		write(http.StatusOK, map[string]any{"schema_version": schema, "game": g.game, "server_time": time.Now().UTC(),
			"assets": map[string]any{"readable": true, "access_path": "public"}})
	case path == aub.HostedGamePrefix+"/game1/join-content":
		write(http.StatusOK, map[string]any{"schema_version": schema, "game_id": "game1", "map_revision": 3,
			"endpoint_key": "203.0.113.4:26000", "package": g.pkg})
	case strings.HasPrefix(path, aub.HostedGamePrefix+"/game1/join-content/files/"):
		body, ok := g.bodies[strings.TrimPrefix(path, aub.HostedGamePrefix+"/game1/join-content/files/")]
		if !ok {
			write(http.StatusNotFound, map[string]any{"message": "no such file"})
			return
		}
		sum := sha256.Sum256(body)
		w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
		_, _ = w.Write(body)
	case path == aub.HostedGamePrefix+"/game1/join":
		g.minted++
		id := "ticket" + strings.Repeat("x", g.minted)
		g.tickets[id] = true
		write(http.StatusCreated, map[string]any{"schema_version": schema, "ticket": map[string]any{
			"ticket_id": id, "link": "autopigeon://join/" + id, "game_id": "game1", "expires_in_seconds": 120}})
	case strings.HasPrefix(path, aub.HostedGamePrefix+"/join-tickets/"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, aub.HostedGamePrefix+"/join-tickets/"), "/resolve")
		g.redeemed = append(g.redeemed, id)
		if !g.tickets[id] {
			write(http.StatusConflict, map[string]any{"message": "That join link has already been used.",
				"data": map[string]any{"reason": map[string]any{"code": "hosted_game_join_ticket_already_redeemed"}}})
			return
		}
		delete(g.tickets, id)
		write(http.StatusOK, map[string]any{"schema_version": schema, "game_id": "game1", "title": "Chapel night",
			"host_nickname": "Vera", "endpoint": "203.0.113.4:26000", "endpoint_host": "203.0.113.4",
			"endpoint_port": 26000, "endpoint_key": "203.0.113.4:26000", "reachability": "unverified",
			"game_family": "quake1", "engine_runtime": g.game["engine_runtime"], "map_id": "map1", "map_revision": 3,
			"package_sha256": g.pkg.PackageSHA256, "assets": map[string]any{"readable": true},
			"join_content": g.game["join_content"], "engine_action": "join_server"})
	default:
		write(http.StatusNotFound, map[string]any{"message": "no route " + path})
	}
}

func gamesMachine(t *testing.T) (*machine, *gameFixture) {
	t.Helper()
	m := newMachine(t)
	fixture := newGameFixture(t)
	m.backend.games = fixture

	return m, fixture
}

func steps(t *testing.T, body map[string]any) (state, next string) {
	t.Helper()
	readiness, _ := body["readiness"].(map[string]any)
	state, _ = readiness["state"].(string)
	next, _ = readiness["next"].(string)

	return state, next
}

func TestGamesNeedAnAccountAndLocalWorkDoesNot(t *testing.T) {
	m, _ := gamesMachine(t)
	status, body := m.call(http.MethodGet, "/api/v1/games", nil)
	if status != http.StatusUnauthorized || body["code"] != codeSignInRequired {
		t.Fatalf("signed out games = %d %v", status, body)
	}
	for _, path := range []string{"/api/v1/jobs", "/api/v1/build/pipelines", "/api/v1/engines"} {
		if status, body := m.call(http.MethodGet, path, nil); status != http.StatusOK {
			t.Fatalf("%s signed out = %d: %v; local work must not need an account", path, status, body["error"])
		}
	}
}

// The whole setup, one real action at a time, each re-checked, then one join.
func TestAGameIsSetUpStepByStepAndJoinedOnce(t *testing.T) {
	m, fixture := gamesMachine(t)
	m.signIn()

	status, body := m.call(http.MethodGet, "/api/v1/games", nil)
	if status != http.StatusOK {
		t.Fatalf("list = %d %v", status, body)
	}
	status, body = m.call(http.MethodGet, "/api/v1/games/game1", nil)
	if state, next := steps(t, body); status != http.StatusOK || state != "setup_required" || next != "engine_profile" {
		t.Fatalf("first read = %d %s next %s: %v", status, state, next, body["error"])
	}
	readiness := body["readiness"].(map[string]any)
	for _, forbidden := range []string{fixture.pkg.PackageSHA256} {
		for _, raw := range readiness["steps"].([]any) {
			step := raw.(map[string]any)
			if strings.Contains(step["detail"].(string), forbidden) || strings.Contains(step["title"].(string), forbidden) {
				t.Fatalf("normal copy shows a digest: %v", step)
			}
		}
	}

	// 1. What the engine profile may do, then the approval against that digest.
	status, body = m.call(http.MethodGet, "/api/v1/games/game1/engine-profile/permissions", nil)
	if status != http.StatusOK || body["report"] == "" {
		t.Fatalf("permissions = %d %v", status, body)
	}
	status, body = m.call(http.MethodPost, "/api/v1/games/game1/engine-profile/approve",
		map[string]any{"digest": body["digest"]})
	if state, next := steps(t, body); status != http.StatusOK || next != "engine_executable" {
		t.Fatalf("after approval = %d %s next %s: %v", status, state, next, body["error"])
	}

	// 2 and 3. The program and the folders, through the existing bind route.
	self, _ := os.Executable()
	if status, body = m.call(http.MethodPost, "/api/v1/profiles/"+enginefixture.ProfileID+"/bind",
		map[string]any{"executables": map[string]string{"engine": self}}); status != http.StatusOK {
		t.Fatalf("bind program = %d %v", status, body["error"])
	}
	if status, body = m.call(http.MethodPost, "/api/v1/profiles/"+enginefixture.ProfileID+"/bind",
		map[string]any{"roots": map[string]string{"game_root": m.gameRoot, "content_root": m.content}}); status != http.StatusOK {
		t.Fatalf("bind folders = %d %v", status, body["error"])
	}
	status, body = m.call(http.MethodGet, "/api/v1/games/game1", nil)
	if state, next := steps(t, body); next != "join_content" {
		t.Fatalf("after binding = %s next %s", state, next)
	}

	// 4. The map files: no ticket is spent on setup.
	status, body = m.call(http.MethodPost, "/api/v1/games/game1/content", map[string]any{})
	if state, _ := steps(t, body); status != http.StatusOK || state != "ready_for_review" {
		t.Fatalf("after download = %d %s: %v", status, state, body["error"])
	}
	if fixture.minted != 0 || len(fixture.redeemed) != 0 {
		t.Fatalf("setup minted %d and redeemed %v", fixture.minted, fixture.redeemed)
	}

	// 5. The review spends one fresh ticket and shows the command.
	status, body = m.call(http.MethodPost, "/api/v1/games/game1/review", map[string]any{})
	if status != http.StatusOK || body["plan_id"] == "" || body["preview"] == nil {
		t.Fatalf("review = %d %v", status, body)
	}
	if fixture.minted != 1 || len(fixture.redeemed) != 1 {
		t.Fatalf("review minted %d, redeemed %v", fixture.minted, fixture.redeemed)
	}
	planID := body["plan_id"]
	jobsBefore := len(m.jobIDs())

	if status, _ = m.call(http.MethodPost, "/api/v1/games/game1/launch",
		map[string]any{"plan_id": planID, "approve": false}); status == http.StatusOK {
		t.Fatal("an unapproved launch started")
	}
	status, body = m.call(http.MethodPost, "/api/v1/games/game1/launch", map[string]any{"plan_id": planID, "approve": true})
	if status != http.StatusOK {
		t.Fatalf("launch = %d %v", status, body)
	}
	started := body["job"].(map[string]any)
	request := started["request"].(map[string]any)
	roots := request["roots"].(map[string]any)
	if !strings.Contains(roots["game_root"].(string), filepath.Join("join-content")) ||
		!strings.HasPrefix(request["runtime"].(map[string]any)["mod_name"].(string), "ap-") {
		t.Fatalf("the join does not load the staged map files: %v", request)
	}
	// The same approval again is not a second game.
	if status, body = m.call(http.MethodPost, "/api/v1/games/game1/launch",
		map[string]any{"plan_id": planID, "approve": true}); status != http.StatusConflict {
		t.Fatalf("replayed approval = %d %v", status, body)
	}
	if got := len(m.jobIDs()); got != jobsBefore+1 {
		t.Fatalf("%d jobs after one approval, want %d", got, jobsBefore+1)
	}
	m.waitForJob(started["id"].(string))

	// The owned game folder gained nothing.
	entries, _ := os.ReadDir(m.gameRoot)
	if len(entries) != 1 {
		t.Fatalf("the game folder changed: %v", entries)
	}
}

func TestAGameThatEndsDuringSetupIsNotReadyAndSpendsNothing(t *testing.T) {
	m, fixture := gamesMachine(t)
	m.signIn()
	fixture.mu.Lock()
	fixture.game["state"], fixture.game["joinable"], fixture.game["lifecycle_reason"] = "offline", false, "host_stopped"
	fixture.mu.Unlock()

	status, body := m.call(http.MethodGet, "/api/v1/games/game1", nil)
	if state, next := steps(t, body); status != http.StatusOK || state != "not_joinable" || next != "game" {
		t.Fatalf("ended game = %d %s %s", status, state, next)
	}
	if status, body = m.call(http.MethodPost, "/api/v1/games/game1/review", map[string]any{}); status != http.StatusConflict ||
		body["code"] != "game_changed" {
		t.Fatalf("review of an ended game = %d %v", status, body)
	}
	if fixture.minted != 0 {
		t.Fatal("a ticket was minted for an ended game")
	}
}

func TestAPendingLinkIsRedeemedOnceAndStartsNothing(t *testing.T) {
	m, fixture := gamesMachine(t)
	m.signIn()
	fixture.mu.Lock()
	fixture.tickets["tktfromaug"] = true
	fixture.mu.Unlock()

	dir := filepath.Join(m.dir, "config")
	if err := joinintent.Receive(joinintent.Path(dir), "tktfromaug", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	jobsBefore := len(m.jobIDs())
	for index := 0; index < 3; index++ {
		status, body := m.call(http.MethodGet, "/api/v1/games/pending", nil)
		pending, _ := body["pending"].(map[string]any)
		if status != http.StatusOK || pending["game_id"] != "game1" {
			t.Fatalf("pending #%d = %d %v", index, status, body)
		}
	}
	if len(fixture.redeemed) != 1 {
		t.Fatalf("redeemed %v; a link is spent once", fixture.redeemed)
	}
	if len(m.jobIDs()) != jobsBefore {
		t.Fatal("opening a link started a job")
	}
	raw, _ := os.ReadFile(joinintent.Path(dir))
	if strings.Contains(string(raw), "tktfromaug") {
		t.Fatal("the spent ticket is still on disk")
	}
}

func TestAnEnginePublicationThatChangedSinceTheReviewIsNotInstalled(t *testing.T) {
	m, fixture := gamesMachine(t)
	m.signIn()

	publish := func(summary string) {
		document, err := profile.DecodeEngine(enginefixture.ProfileJSON)
		if err != nil {
			t.Fatal(err)
		}
		document.ID, document.Name, document.Runtime = "aucom.fixture.published-engine", "Published fixture engine", "published-fixture"
		document.Summary = summary
		canonical, err := profile.Export(document)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(canonical)
		fixture.mu.Lock()
		fixture.listing = &publishedFixture{id: "listing1", document: canonical, digest: "sha256:" + hex.EncodeToString(sum[:])}
		fixture.game["engine_runtime"] = "published-fixture"
		fixture.game["engine_profile_id"] = "listing1"
		fixture.game["engine_profile_ref"] = "aucom.fixture.published-engine"
		fixture.game["engine_profile_version"] = "1.0.0"
		fixture.mu.Unlock()
	}
	publish("The first publication.")

	status, body := m.call(http.MethodGet, "/api/v1/games/game1", nil)
	readiness := body["readiness"].(map[string]any)
	engine := readiness["engine"].(map[string]any)
	if state, next := steps(t, body); status != http.StatusOK || next != "engine_profile" || engine["listing_id"] != "listing1" {
		t.Fatalf("missing engine = %d %s %s %v", status, state, next, engine)
	}
	status, body = m.call(http.MethodPost, "/api/v1/games/game1/engine-profile/review", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("review = %d %v", status, body)
	}
	review := body["review"].(map[string]any)
	if review["trust"] != "community" || review["deployment_trust"] != "verified" {
		t.Fatalf("review trust = %v; a deployment's word never becomes this machine's", review)
	}

	publish("A second publication under the same version.")
	status, body = m.call(http.MethodPost, "/api/v1/games/game1/engine-profile/install",
		map[string]any{"digest": review["digest"], "approve": true})
	if status != http.StatusConflict || body["code"] != "publication_changed" {
		t.Fatalf("stale install = %d %v", status, body)
	}
	if _, err := os.Stat(filepath.Join(m.profiles, "aucom.fixture.published-engine.engine.json")); err == nil {
		t.Fatal("a publication nobody reviewed was written")
	}

	fresh := body["review"].(map[string]any)
	status, body = m.call(http.MethodPost, "/api/v1/games/game1/engine-profile/install",
		map[string]any{"digest": fresh["digest"], "approve": true})
	if status != http.StatusOK {
		t.Fatalf("install = %d %v", status, body)
	}
	set, err := binding.LoadFile(m.bindings)
	if err != nil {
		t.Fatal(err)
	}
	local, found := set.Find("aucom.fixture.published-engine")
	if !found || local.Trust != profile.TrustCommunity || local.Grant == nil || local.ProfileDigest != fresh["digest"] {
		t.Fatalf("installed binding = %+v", local)
	}
	if _, next := steps(t, body); next != "engine_executable" {
		t.Fatalf("after install the next step is %q", next)
	}
}
