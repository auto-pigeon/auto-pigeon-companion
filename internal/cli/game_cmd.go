package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/hostgame"
)

// `companion game` — the community hosted-game lifecycle.
//
// Two halves that must not be confused, and the usage text says so in the order
// a person meets them: finding and joining somebody else's game, and advertising
// one of your own.
//
// Three properties every subcommand here shares.
//
// **Nothing is advertised without the preview having been read.** `host` refuses
// without `--confirm`, and `preview` is what `--confirm` is a confirmation OF: it
// is AUB's own computation of what the listing will say, run without writing, so
// the fields a person approves are the fields that get published.
//
// **Nothing is launched without the command having been shown.** `join` resolves,
// downloads, verifies and prints the exact argv; `--approve` is what starts it.
// The confirmation is a flag rather than a prompt so that a person and a script
// make the same decision the same way, which is `profile install`'s rule.
//
// **This program contacts no address but AUB's.** There is no server browser
// here, no master-server client and no probe: the reachability a listing carries
// is AUB's, established from a machine that is not behind the host's own NAT, and
// a probe from here would establish only that this computer can reach itself.

const gameUsage = `usage:
  companion game list [--mine]                    games being hosted now
  companion game show <game-id>                   one game, in full
  companion game join <link> [--approve]          resolve a join link; --approve to launch
  companion game preview --map=<id> --endpoint=<host:port> [flags]
                                                  what advertising it would disclose
  companion game host --job=<id> --map=<id> --endpoint=<host:port> --confirm [flags]
                                                  advertise a server this machine is running
  companion game stop <game-id> [--reason=<r>]    end an advertisement
`

func runGame(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, gameUsage)

		return 2
	}
	switch args[0] {
	case "list":
		return gameList(env, args[1:])
	case "show":
		return gameShow(env, args[1:])
	case "join":
		return gameJoin(env, args[1:])
	case "preview":
		return gamePreview(env, args[1:])
	case "host":
		return gameHost(env, args[1:])
	case "stop":
		return gameStop(env, args[1:])
	}
	fmt.Fprint(env.Stderr, gameUsage)

	return 2
}

// gameRegistrationFlags is the declaration a host makes, shared by `preview` and
// `host` so the two cannot read the same arguments differently — which is the
// whole value of a preview.
type gameRegistrationFlags struct {
	title    *string
	mapID    *string
	revision *int
	pkg      *string
	family   *string
	slug     *string
	runtime  *string
	version  *string
	mode     *string
	endpoint *string
	region   *string
	maxPlay  *int
	current  *int
	visible  *string
}

func newGameRegistrationFlags(set flagSetter) *gameRegistrationFlags {
	f := &gameRegistrationFlags{
		title:    set.String("title", "", "what to call this game in the listing"),
		mapID:    set.String("map", "", "the AUB map id being played"),
		revision: set.Int("revision", 0, "the exact revision; 0 means whatever is current"),
		pkg:      set.String("package-sha256", "", "the digest of the package this server is serving"),
		family:   set.String("game-family", "quake1", "the game family, in AUB's vocabulary"),
		slug:     set.String("game-slug", "", "the Game Profile slug, when there is one"),
		runtime:  set.String("engine", "", "the engine runtime, e.g. quakespasm"),
		version:  set.String("engine-version", "", "the engine's own version"),
		mode:     set.String("mode", aub.ModeListen, "listen or dedicated"),
		endpoint: set.String("endpoint", "", "the address people connect to, as host:port"),
		region:   set.String("region", "", "where this is, if you want to say"),
		maxPlay:  set.Int("max-players", 0, "how many people the server accepts"),
		current:  set.Int("players", 0, "how many are in it now"),
		visible:  set.String("visibility", aub.GamePrivate, "private, unlisted or public"),
	}

	return f
}

// flagSetter is the subset of *flag.FlagSet these flags need. An interface so the
// helper can be built against a test's own set without importing the CLI's
// plumbing into a test that is about the registration.
type flagSetter interface {
	String(name, value, usage string) *string
	Int(name string, value int, usage string) *int
}

func (f *gameRegistrationFlags) registration(env *Env, settings config.Config) (
	aub.HostedGameRegistration, error,
) {
	if strings.TrimSpace(*f.mapID) == "" {
		return aub.HostedGameRegistration{}, errors.New("--map names the AUB map this server is playing")
	}
	if strings.TrimSpace(*f.runtime) == "" {
		return aub.HostedGameRegistration{}, errors.New(
			"--engine names the engine runtime, so that somebody joining knows what to start")
	}
	host, port, err := hostgame.SplitEndpoint(*f.endpoint)
	if err != nil {
		return aub.HostedGameRegistration{}, err
	}
	title := strings.TrimSpace(*f.title)
	if title == "" {
		return aub.HostedGameRegistration{}, errors.New(
			"--title names the game in the listing; a listing with no name is one nobody can tell from another")
	}

	root, err := installRoot(env, settings)
	if err != nil {
		return aub.HostedGameRegistration{}, err
	}

	return aub.HostedGameRegistration{
		Title:             title,
		MapID:             strings.TrimSpace(*f.mapID),
		MapRevision:       *f.revision,
		PackageSHA:        strings.TrimSpace(*f.pkg),
		GameFamily:        strings.TrimSpace(*f.family),
		GameSlug:          strings.TrimSpace(*f.slug),
		EngineRuntime:     strings.TrimSpace(*f.runtime),
		EngineVersion:     strings.TrimSpace(*f.version),
		Mode:              strings.TrimSpace(*f.mode),
		EndpointHost:      host,
		EndpointPort:      port,
		Region:            strings.TrimSpace(*f.region),
		PlayersCurrent:    *f.current,
		PlayersMax:        *f.maxPlay,
		PlayersObservable: *f.maxPlay > 0 || *f.current > 0,
		Visibility:        strings.TrimSpace(*f.visible),
		ProcessIdentity:   hostgame.ProcessIdentity(root, strings.TrimSpace(*f.mapID), host, port),
		ClientVersion:     "aucom/" + env.Version,
	}, nil
}

// installRoot is what ProcessIdentity is anchored to: this installation's own
// configuration directory. A `--config` pointing somewhere deliberate is a
// different installation for this purpose, which is what makes two test stacks on
// one machine two hosts rather than one that keeps reclaiming the other's lease.
func installRoot(env *Env, settings config.Config) (string, error) {
	if env.ConfigPath != "" {
		return env.ConfigPath, nil
	}
	_ = settings

	return config.Dir()
}

func gamePreview(env *Env, args []string) int {
	set := newFlagSet(env, "game preview")
	flags := newGameRegistrationFlags(set)
	asJSON := set.Bool("json", false, "print the preview as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	registration, err := flags.registration(env, settings)
	if err != nil {
		return fail(env, err)
	}
	client, err := newClient(settings)
	if err != nil {
		return fail(env, err)
	}
	preview, err := client.PreviewHostedGame(context.Background(), registration)
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, preview)
	}

	return printGamePreview(env, preview)
}

func printGamePreview(env *Env, preview aub.HostedGamePreview) int {
	fmt.Fprintf(env.Stdout, "visibility  %s\n", preview.Visibility)
	fmt.Fprintf(env.Stdout, "audience    %s\n", preview.Audience)
	if preview.EndpointPublished {
		fmt.Fprintf(env.Stdout, "endpoint    %s (%s)\n", preview.Endpoint, preview.EndpointScope)
	} else {
		fmt.Fprintf(env.Stdout, "endpoint    withheld — %s, and nobody outside is shown it\n",
			preview.EndpointScope)
	}
	fmt.Fprintf(env.Stdout, "reachable   %s\n", preview.Reachability)
	fmt.Fprintln(env.Stdout)
	fmt.Fprintln(env.Stdout, "This is everything the listing will say about you:")
	for _, field := range preview.Exposed {
		marker := " "
		if field.Withheld {
			marker = "-"
		}
		fmt.Fprintf(env.Stdout, "  %s %-16s %-52s seen by %s\n",
			marker, field.Field, truncate(field.Value, 52), field.SeenBy)
	}
	if preview.MapWarning != "" {
		fmt.Fprintf(env.Stdout, "\nwarning: %s\n", preview.MapWarning)
	}
	for _, line := range preview.Guidance {
		fmt.Fprintf(env.Stdout, "\n  %s\n", line)
	}
	fmt.Fprintln(env.Stdout, "\nRun the same command as `game host --job=<id> --confirm` to advertise it.")

	return 0
}

func gameHost(env *Env, args []string) int {
	set := newFlagSet(env, "game host")
	flags := newGameRegistrationFlags(set)
	jobID := set.String("job", "", "the supervised job serving this game")
	confirm := set.Bool("confirm", false, "advertise it, having read the preview")
	once := set.Bool("register-only", false, "register and exit without beating (for scripts that beat themselves)")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	if strings.TrimSpace(*jobID) == "" {
		fmt.Fprintln(env.Stderr,
			"error: --job names the supervised job serving this game, so that stopping the server ends the listing")

		return 1
	}
	if !*confirm {
		fmt.Fprintln(env.Stderr,
			"error: run `companion game preview` with the same flags first, then add --confirm.\n"+
				"A game is listed after its host has seen what the listing will say, never before.")

		return 1
	}

	ctx, stop := signalContext()
	defer stop()

	service, settings, err := openJobs(ctx, env, true, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()

	registration, err := flags.registration(env, settings)
	if err != nil {
		return fail(env, err)
	}
	registration.ConfirmExposure = true

	client, err := newClient(settings)
	if err != nil {
		return fail(env, err)
	}

	advertiser := hostgame.New(client, service)
	game, err := advertiser.Start(ctx, registration, hostgame.StartOptions{JobID: *jobID})
	if err != nil {
		return fail(env, err)
	}
	printGameLine(env, game)
	if game.EndpointWithheld {
		fmt.Fprintln(env.Stdout,
			"  the address is on your own network and is shown to nobody outside it")
	}
	if *once {
		fmt.Fprintln(env.Stdout,
			"  registered; nothing is beating for it, so it will expire on this deployment's own clock")

		return 0
	}
	fmt.Fprintf(env.Stdout, "  beating every %s while job %s runs; Ctrl-C to stop\n",
		game.HeartbeatInterval(), *jobID)

	// The loop owns the ending: whichever comes first, the job stopping or this
	// process being interrupted, the lease is ended with the reason that actually
	// applied rather than left for AUB's clock to conclude.
	advertiser.Wait(game.ID)
	for _, advertised := range advertiser.Active() {
		_ = advertised
	}
	fmt.Fprintln(env.Stdout, "  the advertisement has ended")

	return 0
}

func gameStop(env *Env, args []string) int {
	set := newFlagSet(env, "game stop")
	reason := set.String("reason", aub.ReasonHostStopped, "why: host_stopped, host_crashed, owner_signed_out, network_lost")
	rest, code, ok := parseFlags(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, gameUsage)

		return 2
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	client, err := newClient(settings)
	if err != nil {
		return fail(env, err)
	}
	result, err := client.StopHostedGame(context.Background(), rest[0], *reason)
	if err != nil {
		return fail(env, err)
	}
	printGameLine(env, result.Game)

	return 0
}

func gameList(env *Env, args []string) int {
	set := newFlagSet(env, "game list")
	mine := set.Bool("mine", false, "your own advertisements, live and ended")
	asJSON := set.Bool("json", false, "print the page as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	client, err := newClient(settings)
	if err != nil {
		return fail(env, err)
	}
	query := aub.HostedGameQuery{}
	if *mine {
		query.Scope = "mine"
	}
	page, err := client.HostedGames(context.Background(), query)
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, page)
	}
	if len(page.Games) == 0 {
		fmt.Fprintln(env.Stdout, "nobody is hosting a game right now")

		return 0
	}
	for _, game := range page.Games {
		printGameLine(env, game)
	}

	return 0
}

func gameShow(env *Env, args []string) int {
	set := newFlagSet(env, "game show")
	asJSON := set.Bool("json", false, "print the game as JSON")
	rest, code, ok := parseFlags(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, gameUsage)

		return 2
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	client, err := newClient(settings)
	if err != nil {
		return fail(env, err)
	}
	detail, err := client.HostedGameByID(context.Background(), rest[0])
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, detail)
	}
	game := detail.Game
	printGameLine(env, game)
	fmt.Fprintf(env.Stdout, "  map        %s, revision %d\n", nameOr(game.MapName, game.MapID), game.MapRevision)
	fmt.Fprintf(env.Stdout, "  engine     %s %s\n", game.EngineRuntime, game.EngineVersion)
	fmt.Fprintf(env.Stdout, "  mode       %s\n", game.Mode)
	// The occupancy and the address both carry their provenance rather than being
	// printed as facts.
	if game.PlayersObservable {
		fmt.Fprintf(env.Stdout, "  players    %d of %d, as the host reports it\n",
			game.PlayersCurrent, game.PlayersMax)
	} else {
		fmt.Fprintln(env.Stdout, "  players    the host's engine cannot report a count")
	}
	if game.Endpoint != "" {
		fmt.Fprintf(env.Stdout, "  address    %s (%s)\n", game.Endpoint, game.Reachability)
	} else if game.EndpointWithheld {
		fmt.Fprintln(env.Stdout, "  address    withheld — it is on the host's own network")
	}
	fmt.Fprintf(env.Stdout, "  heard from %s ago\n", (time.Duration(game.StaleForMS) * time.Millisecond).Round(time.Second))
	if detail.Assets != nil && !detail.Assets.Readable {
		fmt.Fprintf(env.Stdout, "\n  you cannot download this map: %s\n", detail.Assets.Reason)
	}
	for _, line := range detail.Guidance {
		fmt.Fprintf(env.Stdout, "\n  %s\n", line)
	}

	return 0
}

func gameJoin(env *Env, args []string) int {
	set := newFlagSet(env, "game join")
	approve := set.Bool("approve", false, "run the command, having read it")
	asJSON := set.Bool("json", false, "print the plan as JSON")
	rest, code, ok := parseFlags(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, gameUsage)

		return 2
	}

	ctx, stop := signalContext()
	defer stop()

	service, settings, err := openJobs(ctx, env, *approve, nil)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()

	client, err := newClient(settings)
	if err != nil {
		return fail(env, err)
	}
	syncer, store, err := openSyncer(ctx, env)
	if err != nil {
		return fail(env, err)
	}

	joiner := hostgame.NewJoiner(client, syncer, store, service.Catalog(), service)
	plan, err := joiner.Resolve(ctx, rest[0])
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		if code := printJSON(env, plan); code != 0 || !*approve {
			return code
		}
	} else {
		printJoinPlan(env, plan)
	}
	if !*approve {
		fmt.Fprintln(env.Stdout, "\nNothing has been started. Add --approve to run the command above.")

		return 0
	}
	started, err := joiner.Launch(plan, true)
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintf(env.Stdout, "\nstarted job %s\n", started.ID)

	return 0
}

func printJoinPlan(env *Env, plan hostgame.Plan) {
	resolution := plan.Resolution
	fmt.Fprintf(env.Stdout, "%s\n", resolution.Title)
	if resolution.Host != "" {
		fmt.Fprintf(env.Stdout, "  hosted by  %s\n", resolution.Host)
	}
	fmt.Fprintf(env.Stdout, "  address    %s (%s)\n", resolution.Endpoint, resolution.Reachability)
	fmt.Fprintf(env.Stdout, "  map        %s, revision %d\n",
		nameOr(resolution.MapName, resolution.MapID), resolution.MapRevision)
	fmt.Fprintf(env.Stdout, "  verified   %s\n", plan.MapDigest)
	fmt.Fprintf(env.Stdout, "  engine     %s (%s)\n", plan.EngineProfileID, resolution.EngineRuntime)
	for _, warning := range plan.Warnings {
		fmt.Fprintf(env.Stdout, "\n  note: %s\n", warning)
	}
	if plan.Preview != nil {
		fmt.Fprintln(env.Stdout, "\nThis is what will run:")
		fmt.Fprintf(env.Stdout, "  %s\n", plan.Preview.Shell)
		fmt.Fprintf(env.Stdout, "  in %s\n", plan.Preview.WorkingDir)
	}
}

func printGameLine(env *Env, game aub.HostedGame) {
	state := game.State
	if game.State != "live" {
		state = game.State + " (" + game.Reason + ")"
	}
	fmt.Fprintf(env.Stdout, "%-16s %-28s %-10s %-9s %s\n",
		game.ID, truncate(game.Title, 28), game.Visibility, state,
		nameOr(game.HostNickname, "—"))
}

func nameOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}

	return value
}

func truncate(value string, width int) string {
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}

	return string(runes[:width-1]) + "…"
}
