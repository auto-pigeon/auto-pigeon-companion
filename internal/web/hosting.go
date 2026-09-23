package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/build"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/hostgame"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/joincontent"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/playrun"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// A game hosted from Build & Run is listed in Live Games — the same listing
// AUG's Games page and every Companion read (operator, 2026-09-22: "what you
// start a game with AUCOM, you can see it in AUG > Games").
//
// Nothing new is invented for it. The listing is AUB's hosted_games lease,
// kept alive by the same [hostgame.Advertiser] `companion game host` uses, with
// the finished build uploaded as the join content people download, exactly as
// `game host --build` does. The review previews the listing through AUB's own
// preview route, so what the person confirmed is what AUB publishes; pressing
// Build & Run is that confirmation. The lease ends when the engine's job ends,
// with the reason that applied.
//
// The address players connect to is shown and editable in step 3, never
// silently decided: the host is this machine's address on the network the
// Auto-Pigeon server is reached through, and the port is the one the engine's
// profile declares or, when it declares none, its game's own default — both
// suggestions the person sees before anything is listed. AUB, not this
// program, decides what an address on a home network may be listed as.

// hostingState is the process-wide advertiser and what it did for each run.
type hostingState struct {
	mu         sync.Mutex
	advertiser *hostgame.Advertiser
	backend    string
	listings   map[string]*listingView
}

// listingView is a run's listing as the Activity panel shows it.
type listingView struct {
	State      string `json:"state"` // registering, listed, failed, ended
	Message    string `json:"message,omitempty"`
	GameID     string `json:"game_id,omitempty"`
	Title      string `json:"title,omitempty"`
	Visibility string `json:"visibility,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
	Withheld   bool   `json:"endpoint_withheld,omitempty"`
}

// familyDefaultPorts are each game's own default server port — a protocol
// constant of the game, like HTTP's 80, offered as a suggestion only when an
// engine profile declares no port option of its own.
var familyDefaultPorts = map[string]int{"quake1": 26000, "quake2": 27910, "quake3": 27960}

// hostingActions are the engine actions that serve a game.
var hostingActions = map[string]string{"host_listen": aub.ModeListen, "host_dedicated": aub.ModeDedicated}

func (s *Server) hostingRoutes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/play/listing-defaults": s.handleListingDefaults,
		"POST /api/v1/play/listing-preview": s.handleListingPreview,
	}
}

// handleListingDefaults suggests the address a hosted game is listed at.
func (s *Server) handleListingDefaults(w http.ResponseWriter, r *http.Request) {
	engineID, actionID := r.URL.Query().Get("engine"), r.URL.Query().Get("action")
	document, err := s.engineDocument(engineID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	body := map[string]any{"hosting": hostingActions[actionID] != ""}
	if port, source := suggestedPort(document, actionID); port > 0 {
		body["port"], body["port_source"] = port, source
	}
	if client := s.aubClient(); client != nil {
		if host := outboundHost(client.BaseURL()); host != "" {
			body["host"] = host
		}
	}
	writeJSON(w, http.StatusOK, body)
}

// handleListingPreview asks AUB what the listing would say. Nothing is written.
func (s *Server) handleListingPreview(w http.ResponseWriter, r *http.Request) {
	var body playRequestBody
	if !decodeJSON(w, r, &body) {
		return
	}
	client, ok := s.requireSession(w)
	if !ok {
		return
	}
	registration, err := s.listingRegistration(body.request(""), body.Listing)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	preview, err := client.PreviewHostedGame(r.Context(), registration)
	if err != nil {
		writeError(w, aubStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) engineDocument(id string) (*profile.EngineProfile, error) {
	entry, err := s.engineEntry(strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	document, ok := entry.Profile.(*profile.EngineProfile)
	if !ok {
		return nil, fmt.Errorf("%s is not an engine profile", id)
	}
	return document, nil
}

// suggestedPort is the port an action declares (an option named `port`), or
// the game's own default when it declares none.
func suggestedPort(document *profile.EngineProfile, actionID string) (int, string) {
	for _, action := range document.Actions {
		if action.ID != actionID {
			continue
		}
		for _, option := range action.Options {
			if option.Name == "port" && option.Default != "" {
				if port, err := strconv.Atoi(option.Default); err == nil {
					return port, "profile"
				}
			}
		}
	}
	if port := familyDefaultPorts[document.GameProfile.EngineFamily]; port > 0 {
		return port, "game_default"
	}
	return 0, ""
}

// outboundHost is this machine's address on the route to the Auto-Pigeon
// server: the source address a connection there would use. Nothing is sent —
// a UDP "dial" only picks the route. Derived, never configured: it is the one
// address of this machine that the server's network can be expected to reach.
func outboundHost(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	conn, err := net.DialTimeout("udp", net.JoinHostPort(parsed.Hostname(), port), time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil && !addr.IP.IsLoopback() {
		return addr.IP.String()
	}
	// The server is on this machine (a local stack), so the route to it is
	// loopback — an address nobody else can connect to. This machine's address
	// on its own network is the useful suggestion then.
	return networkAddress()
}

// networkAddress is this machine's first private IPv4 address on an interface
// that is up, or empty.
func networkAddress() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if network, ok := addr.(*net.IPNet); ok {
				if ip := network.IP.To4(); ip != nil && ip.IsPrivate() {
					return ip.String()
				}
			}
		}
	}
	return ""
}

// listingRegistration is the registration a hosted run would make, without
// its join content (the build does not exist yet when the review asks).
func (s *Server) listingRegistration(request playrun.Request, listing *playListingBody) (aub.HostedGameRegistration, error) {
	if listing == nil {
		return aub.HostedGameRegistration{}, errors.New("a listing preview needs the listing: its title, visibility and address")
	}
	mode := hostingActions[request.EngineActionID]
	if mode == "" {
		return aub.HostedGameRegistration{}, errors.New("only a hosted game is listed in Live Games; this action does not host one")
	}
	document, err := s.engineDocument(request.EngineProfileID)
	if err != nil {
		return aub.HostedGameRegistration{}, err
	}
	host := strings.TrimSpace(listing.EndpointHost)
	if host == "" || listing.EndpointPort < 1 || listing.EndpointPort > 65535 {
		return aub.HostedGameRegistration{}, errors.New("a listed game needs the address and port players connect to")
	}
	root, err := s.configDir()
	if err != nil {
		return aub.HostedGameRegistration{}, err
	}
	maxPlayers := 0
	for _, action := range document.Actions {
		if action.ID != request.EngineActionID {
			continue
		}
		for _, option := range action.Options {
			if option.Name == "max_players" {
				maxPlayers, _ = strconv.Atoi(option.Default)
			}
		}
	}
	return aub.HostedGameRegistration{
		Title:            strings.TrimSpace(listing.Title),
		MapID:            request.AssetID,
		MapRevision:      request.RevisionNumber,
		GameFamily:       document.GameProfile.EngineFamily,
		GameSlug:         document.GameProfile.Slug,
		EngineRuntime:    document.Runtime,
		EngineVersion:    document.EngineVersion,
		EngineProfileID:  document.ID,
		EngineProfileVer: document.Version,
		Mode:             mode,
		EndpointHost:     host,
		EndpointPort:     listing.EndpointPort,
		PlayersMax:       maxPlayers,
		Visibility:       strings.TrimSpace(listing.Visibility),
		ProcessIdentity:  hostgame.ProcessIdentity(root, request.AssetID, host, listing.EndpointPort),
		ClientVersion:    "aucom/" + s.version,
	}, nil
}

// engineVersionWait bounds how long a listing waits for the engine to print
// its version. An engine prints it among its first lines, well inside this;
// one that never does is listed with its profile's range instead.
const engineVersionWait = 5 * time.Second

// observedEngineVersion is the version the running engine printed about
// itself, read from its job's output (see hostgame.ObservedEngineVersion). It
// waits briefly for the line to appear and reads at most the first 64 KiB.
func observedEngineVersion(jobs *job.Service, jobID, runtime string) (string, bool) {
	deadline := time.Now().Add(engineVersionWait)
	for {
		if out, err := jobs.Logs(jobID, "stdout", false); err == nil {
			if len(out) > 64<<10 {
				out = out[:64<<10]
			}
			if version, ok := hostgame.ObservedEngineVersion(runtime, string(out)); ok {
				return version, true
			}
		}
		if current, err := jobs.Get(jobID); err != nil || current.State.Terminal() || time.Now().After(deadline) {
			return "", false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// playLaunched lists a hosted run once its engine is running. It returns at
// once: the upload and the registration happen beside the run, and their
// outcome is shown in Activity.
func (s *Server) playLaunched(record playrun.Record) {
	if record.Request.Listing == nil || record.Launch == nil || hostingActions[record.Request.EngineActionID] == "" {
		return
	}
	s.hosting.set(record.ID, &listingView{State: "registering", Title: record.Request.Listing.Title,
		Message: "Uploading the map for people who join, then listing it in Live Games…"})
	go s.advertiseRun(record)
}

func (s *Server) advertiseRun(record playrun.Record) {
	fail := func(err error) {
		s.logf("run %s: listing: %v", record.ID, err)
		s.hosting.set(record.ID, &listingView{State: "failed", Title: record.Request.Listing.Title,
			Message: "The game is running but could not be listed: " + err.Error()})
	}
	client := s.aubClient()
	if client == nil || !client.Authenticated() {
		fail(errors.New("sign in to list a game"))
		return
	}
	jobs, err := s.requireJobsService()
	if err != nil {
		fail(err)
		return
	}
	listing := record.Request.Listing
	registration, err := s.listingRegistration(record.Request, &playListingBody{
		Title: listing.Title, Visibility: listing.Visibility,
		EndpointHost: listing.EndpointHost, EndpointPort: listing.EndpointPort,
	})
	if err != nil {
		fail(err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// The join content: the finished build's level, as `game host --build`
	// uploads it, so people joining download exactly what is being played.
	if record.BuildID != "" {
		pkg, err := s.uploadJoinContent(ctx, client, record)
		if err != nil {
			fail(fmt.Errorf("uploading the map for people who join: %w", err))
			return
		}
		registration.PackageSHA, registration.MapRevision = pkg.PackageSHA256, pkg.MapRevision
		registration.ContentRequirement = "package"
	}
	registration.ConfirmExposure = true
	if version, ok := observedEngineVersion(jobs, record.Launch.JobID, registration.EngineRuntime); ok {
		registration.EngineVersion = version
	}

	advertiser := s.hosting.advertiserFor(client, jobs)
	game, err := advertiser.Start(context.Background(), registration, hostgame.StartOptions{JobID: record.Launch.JobID})
	if err != nil {
		fail(err)
		return
	}
	view := &listingView{
		State: "listed", GameID: game.ID, Title: game.Title, Visibility: game.Visibility,
		Endpoint: net.JoinHostPort(registration.EndpointHost, strconv.Itoa(registration.EndpointPort)),
		Withheld: game.EndpointWithheld,
	}
	if game.Visibility != registration.Visibility {
		view.Message = fmt.Sprintf("Listed as %s: the Auto-Pigeon server decides what an address on your own network may be.",
			game.Visibility)
	}
	s.hosting.set(record.ID, view)
	s.logf("run %s: listed as game %s (%s)", record.ID, game.ID, game.Visibility)

	advertiser.Wait(game.ID)
	s.hosting.set(record.ID, &listingView{State: "ended", GameID: game.ID, Title: game.Title,
		Visibility: game.Visibility, Message: "The game stopped, so its listing has ended."})
}

func (s *Server) uploadJoinContent(ctx context.Context, client *aub.Client, record playrun.Record) (aub.JoinPackage, error) {
	dir, err := s.buildsDir()
	if err != nil {
		return aub.JoinPackage{}, err
	}
	manifest, err := build.Find(dir, record.BuildID)
	if err != nil {
		return aub.JoinPackage{}, err
	}
	level, err := build.PlayableLevel(manifest)
	if err != nil {
		return aub.JoinPackage{}, err
	}
	document, err := s.engineDocument(record.Request.EngineProfileID)
	if err != nil {
		return aub.JoinPackage{}, err
	}
	built, err := joincontent.Build(joincontent.Level{MapName: record.Request.MapName, BSP: level.BSP, Lit: level.Lit},
		record.Request.AssetID, record.Request.RevisionNumber, document.GameProfile.EngineFamily)
	if err != nil {
		return aub.JoinPackage{}, err
	}
	return joincontent.Upload(ctx, client.WithTimeout(10*time.Minute), built)
}

func (h *hostingState) set(runID string, view *listingView) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.listings == nil {
		h.listings = map[string]*listingView{}
	}
	h.listings[runID] = view
}

func (h *hostingState) view(runID string) *listingView {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.listings[runID]
}

// advertiserFor is the one advertiser for the server in use. A different
// server gets a new one; the old one's leases end with their jobs.
func (h *hostingState) advertiserFor(client *aub.Client, jobs hostgame.Jobs) *hostgame.Advertiser {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.advertiser == nil || h.backend != client.BaseURL() {
		h.advertiser, h.backend = hostgame.New(client, jobs), client.BaseURL()
	}
	return h.advertiser
}
