package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/assetsync"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/build"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/engine"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/hostgame"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/joincontent"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/joinintent"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/joinready"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/web"
)

// Joining from the terminal — `AUB/AUG/AUCOM/AUT 244F`.
//
// Every command here renders internal/joinready's report or acts through
// internal/hostgame's Joiner, which is what the page uses too. There is no
// terminal-only notion of "ready".

// JoinContentTimeout bounds one join-content file transfer. Longer than an API
// call's, because a compiled map is megabytes, and bounded because a transfer
// that never ends is a setup that never ends.
const JoinContentTimeout = 10 * time.Minute

// openJoiner builds the joiner every command here uses.
func openJoiner(ctx context.Context, env *Env, started bool) (*hostgame.Joiner, *job.Service, config.Config, error) {
	service, settings, err := openJobs(ctx, env, started, nil)
	if err != nil {
		return nil, nil, settings, err
	}
	joiner, err := joinerFor(env, settings, service)
	if err != nil {
		service.Close()

		return nil, nil, settings, err
	}

	return joiner, service, settings, nil
}

func joinerFor(env *Env, settings config.Config, service *job.Service) (*hostgame.Joiner, error) {
	if !settings.Session.Valid() {
		return nil, errors.New("not signed in: games are listed by your account. " +
			"Run `companion auth login --email <address>`; building and running locally need no account")
	}
	client, err := newClient(settings)
	if err != nil {
		return nil, err
	}
	_, _, bindingsPath, err := statePaths(env, settings)
	if err != nil {
		return nil, err
	}
	stager, err := joinStager(settings)
	if err != nil {
		return nil, err
	}
	local := joinready.Local{
		Catalog: service.Catalog(),
		Bindings: func() (*binding.Set, error) {
			set, err := binding.LoadFile(bindingsPath)
			if errors.Is(err, binding.ErrNoFile) {
				return binding.NewSet(), nil
			}

			return set, err
		},
		Checker: engine.Checker{Platform: currentPlatform()},
		Stager:  stager,
	}

	return hostgame.NewJoiner(client.WithTimeout(JoinContentTimeout), local, service), nil
}

// joinStager is the join-content stage under the asset cache.
func joinStager(settings config.Config) (*joincontent.Stager, error) {
	dir, err := settings.AssetCache()
	if err != nil {
		return nil, err
	}
	store, err := assetsync.Open(dir)
	if err != nil {
		return nil, err
	}

	return &joincontent.Stager{Root: filepath.Join(dir, "join-content"), Store: store}, nil
}

func gameReady(env *Env, args []string) int {
	set := newFlagSet(env, "game ready")
	asJSON := set.Bool("json", false, "print the report as JSON")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, gameUsage)

		return 2
	}
	ctx, stop := signalContext()
	defer stop()
	joiner, service, _, err := openJoiner(ctx, env, false)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()
	report, err := joiner.Assess(ctx, rest[0])
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, report)
	}
	printReport(env, report)
	if report.State != joinready.StateReadyForReview {
		return 3
	}

	return 0
}

func gameFetch(env *Env, args []string) int {
	set := newFlagSet(env, "game fetch")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, gameUsage)

		return 2
	}
	ctx, stop := signalContext()
	defer stop()
	joiner, service, _, err := openJoiner(ctx, env, false)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()
	report, err := joiner.DownloadContent(ctx, rest[0], func(done, total int) {
		fmt.Fprintf(env.Stdout, "  fetched %d of %d files\n", done, total)
	})
	if err != nil {
		return fail(env, err)
	}
	printReport(env, report)

	return 0
}

func gameJoin(env *Env, args []string) int {
	set := newFlagSet(env, "game join")
	approve := set.Bool("approve", false, "run the command, having read it")
	gameID := set.String("game", "", "join this game with a fresh link instead of one you were given")
	asJSON := set.Bool("json", false, "print the plan as JSON")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if (len(rest) == 1) == (strings.TrimSpace(*gameID) != "") {
		fmt.Fprint(env.Stderr, gameUsage)

		return 2
	}

	ctx, stop := signalContext()
	defer stop()
	joiner, service, _, err := openJoiner(ctx, env, *approve)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()

	var plan hostgame.Plan
	if *gameID != "" {
		plan, err = joiner.Prepare(ctx, strings.TrimSpace(*gameID))
	} else {
		plan, err = joiner.Resolve(ctx, rest[0])
	}
	if err != nil {
		if plan.Readiness.SchemaVersion != "" && !*asJSON {
			printReport(env, plan.Readiness)
		}

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
	if errors.Is(err, hostgame.ErrAlreadyJoining) {
		fmt.Fprintf(env.Stdout, "\nthis game is already running as job %s\n", started.ID)

		return 0
	}
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintf(env.Stdout, "\nstarted job %s; Ctrl-C stops the game\n", started.ID)
	// The executor is in this process: returning would stop the engine. So a
	// terminal join lasts as long as the game does, the way `job run` does.
	finished, err := service.Wait(ctx, started.ID)
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintf(env.Stdout, "job %s %s\n", finished.ID, finished.State)

	return 0
}

func printReport(env *Env, report joinready.Report) {
	title := report.Title
	if title == "" {
		title = "this game"
	}
	fmt.Fprintf(env.Stdout, "%s — %s\n", title, strings.ReplaceAll(report.State, "_", " "))
	for _, step := range report.Steps {
		mark := "  "
		switch {
		case step.Done:
			mark = "ok"
		case step.ID == report.Next:
			mark = "->"
		}
		fmt.Fprintf(env.Stdout, "  %s %-24s %s\n", mark, step.Title, step.Detail)
		if step.Warning != "" {
			fmt.Fprintf(env.Stdout, "     note: %s\n", step.Warning)
		}
	}
	if report.Next != "" {
		fmt.Fprintln(env.Stdout, "\n  Set up what is marked -> first: the Games area of the Companion walks through it,")
		fmt.Fprintln(env.Stdout, "  or use `companion engine bind`, `companion game fetch` and `companion profile grant`.")
	}
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
	switch {
	case plan.PackageSHA256 != "":
		fmt.Fprintf(env.Stdout, "  map files  downloaded and checked (%s)\n", joincontent.Short(plan.PackageSHA256))
	case resolution.JoinContent.State == aub.JoinContentNotRequired:
		fmt.Fprintln(env.Stdout, "  map files  none needed, the host says")
	default:
		fmt.Fprintln(env.Stdout, "  map files  the host did not say; nothing was checked")
	}
	fmt.Fprintf(env.Stdout, "  engine     %s (%s)\n", plan.Readiness.Engine.Name, resolution.EngineRuntime)
	for _, warning := range plan.Warnings {
		fmt.Fprintf(env.Stdout, "\n  note: %s\n", warning)
	}
	if plan.Preview != nil {
		fmt.Fprintln(env.Stdout, "\nThis is what will run:")
		fmt.Fprintf(env.Stdout, "  %s\n", plan.Preview.Shell)
		fmt.Fprintf(env.Stdout, "  in %s\n", plan.Preview.WorkingDir)
	}
}

// gameOpen is what the registered `autopigeon://` handler runs.
//
// It validates the link's shape BEFORE anything else, records it for the page
// through internal/joinintent, and starts or raises the Companion's own page. It
// makes no network request, redeems nothing, and starts no game: the page redeems
// the link once, shows what this computer still needs, and a join still takes a
// fresh review and an approval.
func gameOpen(env *Env, args []string) int {
	set := newFlagSet(env, "game open")
	noBrowser := set.Bool("no-browser", false, "record the link and print the page address, opening nothing")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprint(env.Stderr, gameUsage)

		return 2
	}
	ticketID, err := aub.ParseJoinLink(rest[0])
	if err != nil {
		return fail(env, err)
	}
	dir, err := configDir(env)
	if err != nil {
		return fail(env, err)
	}
	if err = joinintent.Receive(joinintent.Path(dir), ticketID, time.Now().UTC()); err != nil {
		return fail(env, err)
	}

	if address, ok := runningServer(dir); ok {
		page := strings.TrimRight(address, "/") + "/#games"
		fmt.Fprintf(env.Stdout, "the Companion is open at %s\n", page)
		if *noBrowser {
			return 0
		}
		opener := env.OpenBrowser
		if opener == nil {
			opener = web.OpenBrowser
		}
		if err = opener(page); err != nil {
			fmt.Fprintf(env.Stderr, "warning: %v\nopen %s yourself\n", err, page)
		}

		return 0
	}
	if *noBrowser {
		fmt.Fprintln(env.Stdout, "the link is recorded; start the Companion with `companion serve --open` to see it")

		return 0
	}
	executable, err := os.Executable()
	if err != nil {
		return fail(env, err)
	}
	argv := []string{}
	if env.ConfigPath != "" {
		argv = append(argv, "--config", env.ConfigPath)
	}
	argv = append(argv, "serve", "--open", "--interactive", "--open-area=games")
	command := exec.Command(executable, argv...)
	command.SysProcAttr = detachedProcess()
	if err = command.Start(); err != nil {
		return fail(env, fmt.Errorf("starting the Companion: %w", err))
	}
	go command.Process.Release()
	fmt.Fprintln(env.Stdout, "starting the Companion to show this game")

	return 0
}

// runningServer is the address of a Companion page already running for this
// configuration, confirmed by asking it with its own token.
func runningServer(dir string) (string, bool) {
	token, err := web.ReadToken(web.TokenPath(dir))
	if err != nil {
		return "", false
	}
	address, err := web.ReadURL(web.URLPath(dir))
	if err != nil {
		return "", false
	}
	request, err := http.NewRequest(http.MethodGet, strings.TrimRight(address, "/")+"/api/status", nil)
	if err != nil {
		return "", false
	}
	request.Header.Set("X-AUCOM-Token", token)
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", false
	}
	response.Body.Close()

	return address, response.StatusCode == http.StatusOK
}

func configDir(env *Env) (string, error) {
	if env.ConfigPath != "" {
		return filepath.Dir(env.ConfigPath), nil
	}

	return config.Dir()
}

// gamePackage uploads a finished build's map files as a join-content package and
// prints its digest, which `game host --package-sha256` then names.
func gamePackage(env *Env, args []string) int {
	set := newFlagSet(env, "game package")
	buildID := set.String("build", "", "the finished build whose compiled map joiners need")
	mapID := set.String("map", "", "the AUB map the build was made from")
	revision := set.Int("revision", 0, "the exact map revision; 0 means whatever is current")
	family := set.String("game-family", "quake1", "the game family, in AUB's vocabulary")
	asJSON := set.Bool("json", false, "print the stored package as JSON")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	pkg, err := uploadBuildPackage(context.Background(), env, settings, *buildID, *mapID, *revision, *family)
	if err != nil {
		return fail(env, err)
	}
	if *asJSON {
		return printJSON(env, pkg)
	}
	fmt.Fprintln(env.Stdout, pkg.PackageSHA256)
	fmt.Fprintf(env.Stdout, "  %d file(s), %d bytes, for revision %d; readable only by people who can see the game\n",
		pkg.FileCount, pkg.TotalBytes, pkg.MapRevision)

	return 0
}

func uploadBuildPackage(ctx context.Context, env *Env, settings config.Config, buildID, mapID string,
	revision int, family string,
) (aub.JoinPackage, error) {
	if strings.TrimSpace(buildID) == "" || strings.TrimSpace(mapID) == "" {
		return aub.JoinPackage{}, errors.New("--build and --map name the build and the map it was made from")
	}
	dir, err := buildsDir(env, settings)
	if err != nil {
		return aub.JoinPackage{}, err
	}
	manifest, err := build.Find(dir, strings.TrimSpace(buildID))
	if err != nil {
		return aub.JoinPackage{}, err
	}
	level, err := build.PlayableLevel(manifest)
	if err != nil {
		return aub.JoinPackage{}, err
	}
	built, err := joincontent.Build(joincontent.Level{MapName: level.MapName, BSP: level.BSP, Lit: level.Lit},
		strings.TrimSpace(mapID), revision, family)
	if err != nil {
		return aub.JoinPackage{}, err
	}
	client, err := newClient(settings)
	if err != nil {
		return aub.JoinPackage{}, err
	}

	return joincontent.Upload(ctx, client.WithTimeout(JoinContentTimeout), built)
}
