// Package joinready is the ONE answer to "can this machine join that game, and if
// not, what is missing" — `AUB/AUG/AUCOM/AUT 244F`.
//
// # One model, three readers
//
// The CLI (`companion game ready`), the local API (`GET /api/v1/games/{id}/readiness`)
// and the Games area of the page all render [Assess]'s [Report]. None of them
// computes a state of its own, because the failure this exists to prevent is two
// surfaces disagreeing about whether somebody is ready — one saying Join while the
// other would have refused.
//
// # Derived, never stored
//
// A report is computed from AUB's current answer about the game and this
// machine's current state: the profile catalog, the local bindings, the engine
// preflight and the join-content stage. Nothing here persists a readiness, so
// nothing can be stale in a way that is not visible — a binding that moved, a
// game that ended or a package that was swapped all show up on the next read.
//
// # Four different things, kept apart
//
//	AUB Game Profile   describes the project and its map dialect; read, never installed
//	Engine Profile     describes how a runtime may be invoked; local, approved per digest
//	local binding      where the runtime and the owned game data are on THIS machine
//	join content       the redistributable client files for the hosted build
//
// Each has its own step, its own states and its own action, and the copy names the
// human object — "vkQuake", "your Quake folder", "the map files for this game" —
// never an id or a digest. The Details carry sanitized provenance for a person who
// wants it.
//
// # "Ready" is earned
//
// [StateReadyForReview] is reported only after the job service — the one that will
// run the command — has produced a preview of it without an error. A report whose
// steps all look done but whose preview failed says so, with the executor's own
// words.
package joinready

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/engine"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/joincontent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// SchemaVersion versions the report.
const SchemaVersion = "aucom.join-readiness/1.0"

// Overall states.
const (
	StateSignInRequired = "sign_in_required"
	StateNotJoinable    = "not_joinable"
	StateSetupRequired  = "setup_required"
	StateReadyForReview = "ready_for_review"
	StateLaunched       = "launched"
)

// Step ids, in the order a person fixes them.
const (
	StepGame           = "game"
	StepGameProfile    = "game_profile"
	StepEngineProfile  = "engine_profile"
	StepEngineProgram  = "engine_executable"
	StepGameContent    = "game_content"
	StepJoinContent    = "join_content"
	StepCommand        = "command"
	joinAction         = profile.ActionJoinServer
	defaultBaseGameDir = "id1"
)

// Step states. Stable words; the copy beside them is for people.
const (
	// game
	GameLive     = "live"
	GameEnded    = "ended"
	GameOwn      = "own_game"
	GameNotFound = "not_found"
	GameStale    = "ticket_stale"
	// game_profile
	ProfileResolved     = "resolved"
	ProfileNotDeclared  = "not_declared"
	ProfileUnresolved   = "unresolved"
	ProfileIncompatible = "incompatible"
	// engine_profile
	EngineReady      = "ready"
	EngineMissing    = "missing"
	EngineChanged    = "changed"
	EngineUnreviewed = "unreviewed"
	EngineUnapproved = "unapproved"
	// engine_executable, game_content
	LocalReady   = "ready"
	LocalMissing = "missing"
	LocalStale   = "stale"
	Blocked      = "blocked"
	// join_content
	ContentNotRequired = "not_required"
	ContentUndeclared  = "undeclared"
	ContentRequired    = "required"
	ContentUnreadable  = "unreadable"
	ContentUnavailable = "unavailable"
	ContentDownloading = "downloading"
	ContentVerified    = "verified"
	ContentStaged      = "staged"
	// command
	CommandBlocked        = "blocked"
	CommandReadyForReview = "ready_for_review"
	CommandPreviewFailed  = "preview_failed"
	CommandLaunched       = "launched"
)

// Actions a step may offer. Only real ones: each names something this program
// can actually do next.
const (
	ActionSignIn          = "sign_in"
	ActionRefresh         = "refresh"
	ActionInstallProfile  = "install_engine_profile"
	ActionApproveProfile  = "approve_engine_profile"
	ActionChooseProgram   = "choose_engine_executable"
	ActionChooseGameRoot  = "choose_game_folder"
	ActionDownloadContent = "download_join_content"
	ActionReview          = "review_join"
)

// Step is one prerequisite.
type Step struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Done   bool   `json:"done"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	// Warning is something true a person should read that does not block.
	Warning string `json:"warning,omitempty"`
	Action  string `json:"action,omitempty"`
}

// Engine is the engine profile a join would use, or would install.
type Engine struct {
	ProfileID string        `json:"profile_id,omitempty"`
	Name      string        `json:"name"`
	Runtime   string        `json:"runtime"`
	Trust     profile.Trust `json:"trust,omitempty"`
	Digest    string        `json:"digest,omitempty"`
	// Executable names the program a person chooses, and BaseDirs the base-game
	// folders the engine reads — both from the document, so a picker can say
	// "choose vkquake" and "choose the folder that contains id1".
	Executable string `json:"executable,omitempty"`
	// ExecutableName is the declared name a binding records the program under.
	ExecutableName string   `json:"executable_name,omitempty"`
	BaseDirs       []string `json:"base_dirs,omitempty"`
	// ListingID and ListingVersion name the exact AUB publication to install when
	// no local profile exists and the host declared one.
	ListingID      string `json:"listing_id,omitempty"`
	ListingVersion string `json:"listing_version,omitempty"`
}

// Details is sanitized provenance: for a Details panel, not for normal copy.
type Details struct {
	GameID          string `json:"game_id"`
	MapID           string `json:"map_id,omitempty"`
	MapRevision     int    `json:"map_revision"`
	GameFamily      string `json:"game_family,omitempty"`
	GameSlug        string `json:"game_slug,omitempty"`
	EngineRuntime   string `json:"engine_runtime,omitempty"`
	HostProfileRef  string `json:"host_engine_profile,omitempty"`
	PackageShort    string `json:"package,omitempty"`
	EndpointKey     string `json:"endpoint_key,omitempty"`
	PlayersSource   string `json:"players_source,omitempty"`
	Reachability    string `json:"reachability,omitempty"`
	ContentIdentity string `json:"content_identity,omitempty"`
}

// Report is the whole answer.
type Report struct {
	SchemaVersion string `json:"schema_version"`
	State         string `json:"state"`
	// Next is the first step that is not done, or empty.
	Next  string `json:"next,omitempty"`
	Steps []Step `json:"steps"`

	Title       string `json:"title"`
	Host        string `json:"host,omitempty"`
	MapName     string `json:"map_name,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
	Players     string `json:"players,omitempty"`
	Freshness   string `json:"freshness,omitempty"`
	Engine      Engine `json:"engine"`
	PackageSize int64  `json:"package_bytes,omitempty"`

	Details Details `json:"details"`

	// Request and Preview are present only in StateReadyForReview. The preview is
	// the job service's own.
	Request *job.Request        `json:"request,omitempty"`
	Preview *job.CommandPreview `json:"preview,omitempty"`

	// Game, Stage and Files are for the caller that goes on to prepare a join;
	// never serialized.
	Game  aub.HostedGame        `json:"-"`
	Stage *joincontent.Stage    `json:"-"`
	Files []aub.JoinContentFile `json:"-"`
	entry *job.CatalogEntry     `json:"-"`
	local binding.LocalBinding  `json:"-"`
}

// Remote is the part of the AUB client an assessment reads.
type Remote interface {
	HostedGameByID(ctx context.Context, gameID string) (aub.HostedGameDetail, error)
	GameProfileBySlug(ctx context.Context, slug string) (aub.GameProfileSummary, error)
	GameJoinContent(ctx context.Context, gameID string) (aub.GameJoinContent, error)
}

// Previewer is the job service's preview.
type Previewer interface {
	Preview(request job.Request) (*job.Job, error)
}

// Local is this machine.
type Local struct {
	Catalog  job.Catalog
	Bindings func() (*binding.Set, error)
	Checker  engine.Checker
	Stager   *joincontent.Stager
	Runner   Previewer
	// Downloading reports a join-content download in progress in this process.
	Downloading func(packageSHA256 string) bool
}

// Assess computes a report for one game.
func Assess(ctx context.Context, remote Remote, local Local, gameID string) (Report, error) {
	report := Report{SchemaVersion: SchemaVersion, Details: Details{GameID: gameID}}
	if remote == nil {
		report.State = StateSignInRequired
		report.Steps = []Step{{ID: StepGame, State: GameNotFound, Title: "Sign in to see this game",
			Detail: "Games are listed by your Auto-Pigeon account. Building and running on this computer work without one.",
			Action: ActionSignIn}}
		report.Next = StepGame

		return report, nil
	}
	detail, err := remote.HostedGameByID(ctx, gameID)
	if err != nil {
		var apiErr *aub.APIError
		if errors.As(err, &apiErr) {
			switch {
			case apiErr.StatusCode == http.StatusUnauthorized:
				return signIn(report), nil
			case apiErr.StatusCode == http.StatusNotFound:
				report.State = StateNotJoinable
				report.Steps = []Step{{ID: StepGame, State: GameNotFound, Title: "This game is not available",
					Detail: "It ended and was removed from your view, or it is not shared with this account."}}
				report.Next = StepGame

				return report, nil
			}
		}

		return report, err
	}
	game := detail.Game
	report.Game = game
	report.Title = game.Title
	report.Host = game.HostNickname
	report.MapName = game.MapName
	report.Endpoint = game.Endpoint
	report.Details = Details{GameID: game.ID, MapID: game.MapID, MapRevision: game.MapRevision,
		GameFamily: game.GameFamily, GameSlug: game.GameSlug, EngineRuntime: game.EngineRuntime,
		HostProfileRef: game.EngineProfileRef, PackageShort: joincontent.Short(game.JoinContent.PackageSHA256),
		EndpointKey: endpointKey(game.Endpoint), PlayersSource: game.PlayersSource,
		Reachability: game.Reachability, ContentIdentity: game.JoinContent.ContentIdentity}
	report.PackageSize = game.JoinContent.TotalBytes
	if game.PlayersObservable {
		report.Players = fmt.Sprintf("%d of %d, as the host reports it", game.PlayersCurrent, game.PlayersMax)
	} else {
		report.Players = "the host's server cannot report a count"
	}
	report.Freshness = freshness(game.StaleForMS)

	report.add(gameStep(game))
	report.add(gameProfileStep(ctx, remote, game))
	report.addEngine(local, game)
	report.addJoinContent(ctx, remote, local, game, detail)
	report.addCommand(local, game)
	report.finish()

	return report, nil
}

func signIn(report Report) Report {
	report.State = StateSignInRequired
	report.Steps = []Step{{ID: StepGame, State: GameNotFound, Title: "Sign in again",
		Detail: "Your session with Auto-Pigeon has ended. Games need your account; local building and running do not.",
		Action: ActionSignIn}}
	report.Next = StepGame

	return report
}

func (r *Report) add(step Step) { r.Steps = append(r.Steps, step) }

func (r *Report) blockedBefore(id string) bool {
	for _, step := range r.Steps {
		if step.ID == id {
			return false
		}
		if !step.Done {
			return true
		}
	}

	return false
}

func (r *Report) finish() {
	r.State = StateReadyForReview
	for _, step := range r.Steps {
		if !step.Done {
			r.Next = step.ID
			r.State = StateSetupRequired
			if step.ID == StepGame {
				r.State = StateNotJoinable
			}

			return
		}
	}
	r.Next = ""
}

func gameStep(game aub.HostedGame) Step {
	switch {
	case game.OwnedByMe:
		return Step{ID: StepGame, State: GameOwn, Title: "This is your own game",
			Detail: "You are hosting it, so there is nothing to join."}
	case !game.Live() || !game.Joinable:
		reason := game.Reason
		if reason == "" {
			reason = "it is no longer live"
		}

		return Step{ID: StepGame, State: GameEnded, Title: "This game has ended",
			Detail: "The host's server is no longer being advertised (" + strings.ReplaceAll(reason, "_", " ") + ")."}
	}

	return Step{ID: StepGame, State: GameLive, Done: true, Title: "The game is running",
		Detail: "Heard from the host " + freshness(game.StaleForMS) + "."}
}

func gameProfileStep(ctx context.Context, remote Remote, game aub.HostedGame) Step {
	step := Step{ID: StepGameProfile, Done: true, Title: "Game"}
	if strings.TrimSpace(game.GameSlug) == "" {
		step.State = ProfileNotDeclared
		step.Detail = "The host named the game family (" + familyName(game.GameFamily) + ") and no project profile."

		return step
	}
	summary, err := remote.GameProfileBySlug(ctx, game.GameSlug)
	if err != nil {
		step.State = ProfileUnresolved
		step.Detail = "The host's game profile is not one your account can read."
		step.Warning = "The engine is still matched by game family, which is what joining needs."

		return step
	}
	if summary.EngineFamily != "" && game.GameFamily != "" && !strings.EqualFold(summary.EngineFamily, game.GameFamily) {
		step.State = ProfileIncompatible
		step.Done = false
		step.Title = "The game does not add up"
		step.Detail = fmt.Sprintf("The host says %s, and its game profile %q is for %s.",
			familyName(game.GameFamily), summary.Name, familyName(summary.EngineFamily))

		return step
	}
	step.State = ProfileResolved
	step.Detail = summary.Name

	return step
}

// addEngine picks the engine profile and reports the three local steps.
func (r *Report) addEngine(local Local, game aub.HostedGame) {
	engineStep := Step{ID: StepEngineProfile, Title: "Engine"}
	programStep := Step{ID: StepEngineProgram, Title: "Engine program", State: Blocked}
	contentStep := Step{ID: StepGameContent, Title: "Your game files", State: Blocked}
	defer func() {
		r.add(engineStep)
		r.add(programStep)
		r.add(contentStep)
	}()

	set, err := local.Bindings()
	if err != nil || set == nil {
		set = binding.NewSet()
	}
	entry, candidates := chooseEngine(local, set, game)
	r.Engine = Engine{Runtime: game.EngineRuntime, Name: runtimeName(game.EngineRuntime)}
	if entry == nil {
		engineStep.State = EngineMissing
		if game.EngineProfileID != "" {
			r.Engine.ListingID = game.EngineProfileID
			r.Engine.ListingVersion = game.EngineProfileVer
			engineStep.Detail = "The host's " + r.Engine.Name + " profile is published on Auto-Pigeon. " +
				"Review it and install it on this computer."
			engineStep.Action = ActionInstallProfile
		} else {
			engineStep.Detail = "This computer has no engine profile for " + r.Engine.Name + ". " +
				describeInstalled(candidates)
		}
		programStep.Detail = "Set up after the engine."
		contentStep.Detail = "Set up after the engine."

		return
	}
	document := entry.Profile.(*profile.EngineProfile)
	localBinding, _ := set.Find(document.ID)
	r.entry = entry
	r.local = localBinding
	r.Engine = Engine{ProfileID: document.ID, Name: document.Name, Runtime: document.Runtime,
		Trust: entry.Trust, Digest: entry.Digest, BaseDirs: baseDirs(document)}
	if action, ok := document.ActionByID(joinAction); ok {
		r.Engine.Executable = executableTitle(document, action.Executable)
		r.Engine.ExecutableName = action.Executable
	}
	if !strings.EqualFold(document.GameProfile.EngineFamily, game.GameFamily) && game.GameFamily != "" {
		engineStep.State = EngineMissing
		engineStep.Detail = document.Name + " on this computer is for " + familyName(document.GameProfile.EngineFamily) +
			", and this game is " + familyName(game.GameFamily) + "."

		return
	}

	problems := local.Checker.Check(document, entry.Trust, entry.Digest, localBinding, joinAction)
	engineStep.Title = document.Name
	switch {
	case problems.Has(engine.FaultStaleBinding):
		engineStep.State = EngineChanged
		engineStep.Detail = document.Name + "'s profile changed since you approved it. Review what it asks for now."
		engineStep.Action = ActionApproveProfile
	case problems.Has(engine.FaultNotAuthorized) && localBinding.ProfileID == "":
		engineStep.State = EngineUnreviewed
		engineStep.Detail = "You have not reviewed what " + document.Name + "'s profile may do on this computer."
		engineStep.Action = ActionApproveProfile
	case problems.Has(engine.FaultNotAuthorized):
		engineStep.State = EngineUnapproved
		engineStep.Detail = "Approve what " + document.Name + "'s profile may do on this computer."
		engineStep.Action = ActionApproveProfile
	default:
		engineStep.State = EngineReady
		engineStep.Done = true
		engineStep.Detail = trustSentence(entry.Trust)
	}

	programStep.Title = r.Engine.Executable
	if programStep.Title == "" {
		programStep.Title = document.Name + " program"
	}
	switch {
	case problems.Has(engine.FaultMissingEngine) && strings.TrimSpace(localBinding.Executables[executableName(document)]) == "":
		programStep.State = LocalMissing
		programStep.Detail = "Choose the " + document.Name + " program you already have. Auto-Pigeon does not download engines."
		programStep.Action = ActionChooseProgram
	case problems.Has(engine.FaultMissingEngine):
		programStep.State = LocalStale
		programStep.Detail = "The " + document.Name + " program you chose before is no longer there. Choose it again."
		programStep.Action = ActionChooseProgram
	default:
		programStep.State = LocalReady
		programStep.Done = true
		programStep.Detail = "Chosen on this computer."
	}

	contentStep.Title = "Your " + familyName(game.GameFamily) + " folder"
	switch {
	case problems.Has(engine.FaultUnboundRoot):
		contentStep.State = LocalMissing
		contentStep.Detail = "Choose the folder your own copy of the game is installed in. It is read where it is and never copied or uploaded."
		contentStep.Action = ActionChooseGameRoot
	case problems.Has(engine.FaultMissingRoot) || problems.Has(engine.FaultMissingGameData):
		contentStep.State = LocalStale
		contentStep.Detail = "The game folder you chose before is gone or has no game in it. Choose it again."
		contentStep.Action = ActionChooseGameRoot
	default:
		contentStep.State = LocalReady
		contentStep.Done = true
		contentStep.Detail = "Read in place from the folder you chose."
	}
}

// chooseEngine picks the local engine profile for a game.
//
// The host's runtime is a COMPATIBILITY requirement, never an authority to pick an
// executable: the executable is always this machine's binding. The host's exact
// document id is preferred when this machine has it; otherwise profiles for the
// runtime that offer `join_server`, set-up ones first, then by id so the choice
// is stable.
func chooseEngine(local Local, set *binding.Set, game aub.HostedGame) (*job.CatalogEntry, []string) {
	entries, err := local.Catalog.List()
	if err != nil {
		return nil, nil
	}
	runtime := strings.ToLower(strings.TrimSpace(game.EngineRuntime))
	var installed []string
	var candidates []job.CatalogEntry
	for _, entry := range entries {
		document, isEngine := entry.Profile.(*profile.EngineProfile)
		if !isEngine {
			continue
		}
		if _, offers := document.ActionByID(joinAction); !offers {
			continue
		}
		installed = append(installed, document.Name)
		if strings.EqualFold(strings.TrimSpace(document.Runtime), runtime) {
			candidates = append(candidates, entry)
		}
	}
	if len(candidates) == 0 {
		return nil, installed
	}
	score := func(entry job.CatalogEntry) int {
		document := entry.Profile.(*profile.EngineProfile)
		if game.EngineProfileRef != "" && document.ID == game.EngineProfileRef {
			return 0
		}
		localBinding, _ := set.Find(document.ID)
		if len(local.Checker.Check(document, entry.Trust, entry.Digest, localBinding, joinAction)) == 0 {
			return 1
		}

		return 2
	}
	sort.SliceStable(candidates, func(a, b int) bool {
		sa, sb := score(candidates[a]), score(candidates[b])
		if sa != sb {
			return sa < sb
		}

		return candidates[a].Profile.Metadata().ID < candidates[b].Profile.Metadata().ID
	})
	chosen := candidates[0]

	return &chosen, installed
}

func (r *Report) addJoinContent(ctx context.Context, remote Remote, local Local, game aub.HostedGame,
	detail aub.HostedGameDetail,
) {
	step := Step{ID: StepJoinContent, Title: "Map files for this game"}
	defer func() { r.add(step) }()
	content := detail.Game.JoinContent
	switch content.State {
	case aub.JoinContentNotRequired:
		step.State = ContentNotRequired
		step.Done = true
		step.Detail = "The host says this game needs nothing beyond your own game files."
		if content.ContentIdentity != "" {
			step.Detail += " (" + content.ContentIdentity + ")"
		}

		return
	case aub.JoinContentRequired:
	default:
		step.State = ContentUndeclared
		step.Done = true
		step.Detail = "The host did not say what this game needs."
		step.Warning = "If the host is playing a map you do not have, your engine will stop with “map not found”. " +
			"Nothing was downloaded or checked for this game."

		return
	}
	if content.Readable != nil && !*content.Readable {
		step.State = ContentUnreadable
		step.Detail = "Your account cannot download this game's map. Ask the host to share it with you."

		return
	}
	if !game.Live() {
		step.State = ContentUnavailable
		step.Detail = "The game ended, so its map files are no longer offered."

		return
	}
	served, err := remote.GameJoinContent(ctx, game.ID)
	if err != nil {
		step.State = ContentUnavailable
		step.Detail = "Auto-Pigeon could not offer this game's map files right now."
		var apiErr *aub.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
			step.State = ContentUnreadable
			step.Detail = "Your account cannot download this game's map. Ask the host to share it with you."
		}

		return
	}
	files, err := joincontent.Verify(served.Package, content.PackageSHA256)
	if err != nil {
		step.State = ContentUnavailable
		step.Detail = "The map files offered for this game are not the ones the game names, so they were refused."

		return
	}
	r.Files = files
	if local.Stager == nil {
		step.State = ContentRequired
		step.Detail = "This computer has nowhere to keep map files."

		return
	}
	if stage, err := local.Stager.Lookup(content.PackageSHA256, files); err == nil {
		r.Stage = &stage
		step.State = ContentStaged
		step.Done = true
		step.Detail = "Downloaded, checked and ready (" + humanBytes(content.TotalBytes) + ")."

		return
	}
	switch {
	case local.Downloading != nil && local.Downloading(content.PackageSHA256):
		step.State = ContentDownloading
		step.Detail = "Downloading " + humanBytes(content.TotalBytes) + "…"
	case len(local.Stager.Missing(files)) == 0:
		step.State = ContentVerified
		step.Detail = "Downloaded and checked; not yet set out for the engine."
		step.Action = ActionDownloadContent
	default:
		step.State = ContentRequired
		step.Detail = "Download " + humanBytes(content.TotalBytes) + " of map files for this game. " +
			"Each file is checked before it is used."
		step.Action = ActionDownloadContent
	}
}

// addCommand asks the job service for the exact command, once everything before
// it is done.
func (r *Report) addCommand(local Local, game aub.HostedGame) {
	step := Step{ID: StepCommand, Title: "Start the game", State: CommandBlocked}
	defer func() { r.add(step) }()
	if r.blockedBefore(StepCommand) || r.entry == nil {
		step.Detail = "Available once everything above is done."

		return
	}
	request, err := r.Command(game.Endpoint, local)
	if err != nil {
		step.State = CommandPreviewFailed
		step.Detail = err.Error()

		return
	}
	preview, err := local.Runner.Preview(request)
	switch {
	case err != nil:
		step.State = CommandPreviewFailed
		step.Detail = "The command could not be prepared: " + err.Error()
	case preview.Error != "" || preview.Command == nil:
		step.State = CommandPreviewFailed
		step.Detail = "The command could not be prepared: " + preview.Error
	default:
		step.State = CommandReadyForReview
		step.Done = true
		step.Detail = "Review the exact command before it starts."
		step.Action = ActionReview
		r.Request = &request
		r.Preview = preview.Command
	}
}

// Command builds the join request for an endpoint, against this report's engine
// and stage. It is exported so a join prepared from a fresh ticket builds the
// request the SAME way, with the ticket's endpoint.
func (r *Report) Command(endpoint string, local Local) (job.Request, error) {
	if r.entry == nil {
		return job.Request{}, errors.New("there is no engine to join with")
	}
	host, port, err := SplitEndpoint(endpoint)
	if err != nil {
		return job.Request{}, errors.New("the host's address is not shown to your account, so there is nothing to connect to")
	}
	request := job.Request{
		ProfileID: r.entry.Profile.Metadata().ID,
		ActionID:  joinAction,
		Runtime:   map[string]string{profile.RuntimeServerHost: host, profile.RuntimeServerPort: strconv.Itoa(port)},
		Label:     "Join " + r.Title,
	}
	if r.Stage != nil {
		document := r.entry.Profile.(*profile.EngineProfile)
		action, _ := document.ActionByID(joinAction)
		loadsContent := false
		for _, root := range action.Roots {
			loadsContent = loadsContent || root.Role == profile.RootContent
		}
		if !loadsContent {
			return job.Request{}, errors.New(document.Name + "'s profile cannot load downloaded map files when it " +
				"joins a server, so this game cannot be joined with it")
		}
		gameRoot := r.local.Roots[profile.RootGame]
		if err := r.Stage.Overlay(gameRoot, r.Engine.BaseDirs); err != nil {
			return job.Request{}, err
		}
		request.Roots = map[string]string{
			profile.RootGame:    r.Stage.BaseDir,
			profile.RootContent: r.Stage.GameDirPath,
		}
		request.Runtime[profile.RuntimeModName] = r.Stage.Record.GameDir
	}

	return request, nil
}

// SplitEndpoint parses `host:port`, including a bracketed IPv6 host.
func SplitEndpoint(endpoint string) (string, int, error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(endpoint))
	if err != nil || host == "" {
		return "", 0, fmt.Errorf("%q is not host:port", endpoint)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("%q has no valid port", endpoint)
	}

	return host, port, nil
}

func endpointKey(endpoint string) string {
	host, port, err := SplitEndpoint(endpoint)
	if err != nil {
		return ""
	}

	return strings.ToLower(host) + ":" + strconv.Itoa(port)
}

// EndpointKey is the comparison form of an endpoint, matching AUB's.
func EndpointKey(endpoint string) string { return endpointKey(endpoint) }

func baseDirs(document *profile.EngineProfile) []string {
	var dirs []string
	seen := map[string]bool{}
	for _, layout := range document.ContentLayouts {
		if layout.Root == profile.RootGame && layout.Path != "" && !strings.Contains(layout.Path, "/") &&
			!seen[layout.Path] {
			seen[layout.Path] = true
			dirs = append(dirs, layout.Path)
		}
	}
	if len(dirs) == 0 {
		dirs = []string{defaultBaseGameDir}
	}

	return dirs
}

func executableName(document *profile.EngineProfile) string {
	if action, ok := document.ActionByID(joinAction); ok {
		return action.Executable
	}

	return ""
}

func executableTitle(document *profile.EngineProfile, name string) string {
	for _, executable := range document.Executables {
		if executable.Name == name {
			if executable.Title != "" {
				return executable.Title
			}

			return executable.Name
		}
	}

	return ""
}

func trustSentence(trust profile.Trust) string {
	switch trust {
	case profile.TrustBuiltin:
		return "Included with the Companion."
	case profile.TrustCommunity:
		return "A community profile you approved."
	}

	return "Approved on this computer."
}

func describeInstalled(installed []string) string {
	if len(installed) == 0 {
		return "It has no engine that can join a server."
	}
	sort.Strings(installed)

	return "It has " + strings.Join(unique(installed), ", ") + "."
}

func unique(values []string) []string {
	seen := map[string]bool{}
	out := values[:0]
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}

	return out
}

func runtimeName(runtime string) string {
	names := map[string]string{"vkquake": "vkQuake", "quakespasm": "QuakeSpasm", "ironwail": "Ironwail",
		"fteqw": "FTEQW", "yamagi": "Yamagi Quake II", "ioquake3": "ioquake3"}
	if name, ok := names[strings.ToLower(runtime)]; ok {
		return name
	}
	if runtime == "" {
		return "the host's engine"
	}

	return runtime
}

func familyName(family string) string {
	switch strings.ToLower(family) {
	case "quake1":
		return "Quake"
	case "quake2":
		return "Quake II"
	case "quake3":
		return "Quake III"
	case "":
		return "the game"
	}

	return family
}

func freshness(staleForMS int64) string {
	seconds := staleForMS / 1000
	switch {
	case seconds < 5:
		return "just now"
	case seconds < 120:
		return fmt.Sprintf("%d seconds ago", seconds)
	}

	return fmt.Sprintf("%d minutes ago", seconds/60)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(1<<10))
	}

	return fmt.Sprintf("%d bytes", n)
}
