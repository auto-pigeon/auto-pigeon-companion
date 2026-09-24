package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/incident"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/joinintent"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/release"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/web"
)

// PasswordEnv is the environment variable a password may be supplied in, for
// scripted logins.
//
// Prompting reads a line from stdin without disabling terminal echo: turning
// echo off needs either cgo or golang.org/x/term, and both are excluded here.
// So the password is typed visibly, which is bad enough that this variable and
// a piped stdin exist as the alternatives.
//
// TODO(andrea): revisit if a x/term dependency becomes acceptable — it is
// CGO-free and would fix the echo problem on all six targets.
const PasswordEnv = "AUCOM_PASSWORD"

// LegacyPasswordEnv is the retired Launcher's spelling of PasswordEnv. It is
// still read, so a script written against the Launcher keeps working, but it
// warns: a variable name that silently keeps working forever is a rename that
// never finishes.
const LegacyPasswordEnv = "AUL_PASSWORD"

// loadSettings reads the local config, tolerating a first run with no file.
//
// Every command goes through here, so every command migrates first. That is
// deliberate: a user who runs `companion auth status` after upgrading from the
// Launcher should see their session, not an empty config that a later `serve`
// would have fixed. config.Migrate writes nothing when there is nothing to do,
// so the cost on an already-current machine is two stat calls.
func loadSettings(env *Env) (config.Config, error) {
	if env.ConfigPath != "" {
		// An explicit path is a test or a --config flag pointing somewhere
		// deliberate; migrating the real config directory from under it would
		// be a surprising side effect of asking for a different file.
		settings, err := config.LoadFrom(env.ConfigPath)
		if errors.Is(err, config.ErrNotFound) {
			return settings, nil
		}
		return settings, err
	}

	if report, err := config.Migrate(); err != nil {
		// A migration failure is not fatal to the command being run: the
		// existing config is untouched (nothing is overwritten before its
		// backup succeeds), so the honest response is to say so and carry on
		// with whatever is on disk.
		fmt.Fprintf(env.Stderr, "warning: could not migrate local configuration: %v\n", err)
	} else if report.Performed {
		fmt.Fprint(env.Stderr, report.Summary())
	}

	settings, err := config.Load()
	if errors.Is(err, config.ErrNotFound) {
		return settings, nil
	}
	return settings, err
}

// tokenFilePath is where a running server publishes its API token.
//
// Beside the config file, whichever config file this invocation is using, so a
// `--config` pointing somewhere deliberate does not have its server's token
// land in the real config directory.
func tokenFilePath(env *Env) (string, error) {
	if env.ConfigPath != "" {
		return web.TokenPath(filepath.Dir(env.ConfigPath)), nil
	}
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return web.TokenPath(dir), nil
}

// settingsPath is the config file this invocation writes.
func settingsPath(env *Env) (string, error) {
	if env.ConfigPath != "" {
		return env.ConfigPath, nil
	}
	return config.Path()
}

// updateSettings is the only way this package changes config.json.
//
// Load, mutate and save happen inside one cross-process lock, and mutate is
// handed the file as it is on disk now rather than whatever this command read
// when it started. That is what stops `companion auth login` in one terminal
// from undoing a `catalog_url` a GUI server wrote a moment earlier — the
// signature makes each caller declare the field it is changing instead of
// writing back a whole struct it read minutes ago.
func updateSettings(env *Env, mutate func(*config.Config) error) (config.Config, error) {
	path, err := settingsPath(env)
	if err != nil {
		return config.Config{}, err
	}
	return config.Update(path, mutate)
}

// newClient builds an AUB client carrying whatever session is stored locally.
//
// The address comes from Config.AUB, which reports ErrAUBNotConfigured rather
// than substituting a guessed one — see internal/config.
func newClient(settings config.Config) (*aub.Client, error) {
	baseURL, err := settings.AUB()
	if err != nil {
		return nil, err
	}
	client, err := aub.New(baseURL, nil)
	if err != nil {
		return nil, err
	}
	if settings.Session.Valid() {
		client.SetToken(settings.Session.Token)
	}
	return client, nil
}

// fail prints an error and returns the exit code for a failed operation.
func fail(env *Env, err error) int {
	fmt.Fprintf(env.Stderr, "error: %v\n", err)
	return 1
}

// runVersion prints the version on its first line — the line every harness
// reads — and then what the binary itself records about how it was built, so
// a person holding only an unpacked release can name the exact source commit
// without Go, Git or a checkout.
func runVersion(env *Env, args []string) int {
	set := newFlagSet(env, "version")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	fmt.Fprintln(env.Stdout, env.Version)
	build := release.ReadBuild()
	if build.Commit != "" {
		state := "clean"
		if build.Modified {
			state = "modified: built from uncommitted changes"
		}
		fmt.Fprintf(env.Stdout, "commit %s (%s)\n", build.Commit, state)
	} else {
		fmt.Fprintln(env.Stdout, "commit unknown (built without version control information)")
	}
	fmt.Fprintf(env.Stdout, "built with %s for %s, CGO_ENABLED=%s\n", build.GoVersion, build.Target, build.CGO)
	return 0
}

// runServe starts the local GUI server. With --open it also opens the default
// browser, which is what the no-subcommand invocation does.
//
// Two modes (see internal/web/lifecycle.go). SERVER mode is `serve`'s default
// and `--stay-running`: the process runs until it is interrupted or Quit is
// chosen in the page, whether or not any browser is attached — what scripts,
// harnesses and operators have always started. INTERACTIVE mode
// (`--interactive`, and the no-subcommand launch) is the application: it stops
// once its last page has been closed for the grace period and nothing it
// started is still running.
func runServe(env *Env, args []string) int {
	set := newFlagSet(env, "serve")
	port := set.Int("port", 0, "loopback port to bind; 0 uses the configured port")
	open := set.Bool("open", false, "open the page in the default browser")
	debug := set.Bool("debug", false, "unlock the developer controls: typing any server address in Settings")
	openArea := set.String("open-area", "", "with --open, the area to show first (games)")
	interactive := set.Bool("interactive", false,
		"application mode: stop once the last page has closed and nothing is running")
	stayRunning := set.Bool("stay-running", false,
		"server mode (the default for serve): keep running when every page is closed")
	closeGrace := set.Duration("close-grace", web.DefaultCloseGrace,
		"interactive: how long to wait after the last page closed, for a reload or a crashed tab to come back")
	startupWindow := set.Duration("startup-window", web.DefaultStartupWindow,
		"interactive: how long to wait for the first page before stopping")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	if *interactive && *stayRunning {
		fmt.Fprintln(env.Stderr, "error: --interactive and --stay-running are the two modes; choose one")
		return 2
	}
	if *closeGrace <= 0 || *startupWindow <= 0 {
		fmt.Fprintln(env.Stderr, "error: --close-grace and --startup-window must be positive durations, such as 15s or 3m")
		return 2
	}

	tokenPath, err := tokenFilePath(env)
	if err != nil {
		return fail(env, err)
	}
	configDir := filepath.Dir(tokenPath)

	// Where the server's own lines go. Server mode: stderr, as always.
	// Interactive: the log file, so the terminal keeps to what a person reads.
	var detail io.Writer = env.Stderr
	detailPath := ""
	if *interactive {
		log, path, err := openDetailLog(configDir)
		if err != nil {
			fmt.Fprintf(env.Stderr, "warning: the log file could not be opened (%v); details go to this terminal\n", err)
		} else {
			defer log.Close()
			detail, detailPath = log, path
		}
	}
	logf := timestamped(detail)
	if !*interactive {
		logf = func(format string, args ...any) { fmt.Fprintf(env.Stderr, format+"\n", args...) }
	}

	// A server's incident transcript goes to its log whether or not a backend
	// is configured: there the operator has no other view of what happened.
	if env.incidentState == nil {
		env.incidentState = &incidentHolder{}
	}
	env.incidentState.transcript = true
	if *interactive {
		env.incidentState.detail = detail
	}

	// The extractor runner is built here and resolves LAZILY, so a process that
	// never touches the extractor never hashes or runs it — and a server does
	// not have to before it can listen. See internal/aue.LazyRunner.
	runner := extractorRunner(env, logf)

	ctx, stop := signalContext()
	defer stop()

	// The executor. Started here, so the server's recovery pass runs before it
	// accepts a request and a job left running by a previous crash is marked
	// interrupted rather than reported as still going.
	service, settings, err := openJobs(ctx, env, true, logf)
	if err != nil {
		return fail(env, err)
	}
	defer service.Close()

	chosen := *port
	if chosen == 0 {
		chosen = config.PortOverride()
	}
	if chosen == 0 {
		chosen = settings.Port
	}

	token, err := web.NewToken()
	if err != nil {
		return fail(env, err)
	}
	if err := web.WriteToken(tokenPath, token); err != nil {
		return fail(env, err)
	}
	// Removed on the way out: a token file left behind names a credential for
	// a server that is not listening, and the next `companion job` would try
	// to use it.
	defer func() {
		if err := web.RemoveToken(tokenPath); err != nil {
			fmt.Fprintf(env.Stderr, "warning: %v\n", err)
		}
	}()

	// The same three paths every other command resolves, so a `--config`
	// pointing somewhere deliberate keeps the GUI's profiles, bindings and
	// builds beside it rather than reaching into the real machine's.
	_, profilesDir, bindingsPath, err := statePaths(env, settings)
	if err != nil {
		return fail(env, err)
	}
	buildsPath, err := buildsDir(env, settings)
	if err != nil {
		return fail(env, err)
	}
	assetCache, err := settings.AssetCache()
	if err != nil {
		return fail(env, err)
	}

	// The address is only known after Listen; the notice needs it, so it reads
	// it through this variable.
	var url string
	lifecycle := web.NewLifecycle(web.LifecycleOptions{
		Interactive:   *interactive,
		CloseGrace:    *closeGrace,
		StartupWindow: *startupWindow,
		Notify: func(summary string) {
			logf("lifecycle: no page is open; still running: %s", summary)
			fmt.Fprintf(env.Stdout, "The page is closed. Auto-Pigeon Companion keeps running until this finishes: %s.\n"+
				"Open %s to see it, or press Ctrl+C to stop it now.\n", summary, url)
		},
	})

	server, err := web.NewServer(web.Options{
		Version:   env.Version,
		Debug:     *debug,
		Config:    settings,
		AUE:       runner,
		Jobs:      service,
		Token:     token,
		Lifecycle: lifecycle,
		Logf:      logf,
		Paths: web.Paths{
			Profiles:   profilesDir,
			Bindings:   bindingsPath,
			Builds:     buildsPath,
			AssetCache: assetCache,
			ConfigDir:  configDir,
		},
		UpdateConfig: func(mutate func(*config.Config) error) (config.Config, error) {
			return updateSettings(env, mutate)
		},
		ReadConfig: func() (config.Config, error) {
			path, err := settingsPath(env)
			if err != nil {
				return config.Config{}, err
			}
			return config.LoadFrom(path)
		},
	})
	if err != nil {
		return fail(env, err)
	}
	// Every build, Build & Run and hosted listing this process started stops
	// with it; the job service's own Close (deferred above, so it runs after
	// this) then stops the processes. A Companion that quits leaving a
	// compiler running is a Companion that has lost track of a process the
	// user cannot see.
	defer server.Close()

	listener, err := web.Listen(chosen)
	if err != nil {
		return fail(env, err)
	}
	// Readiness: does the configured AUB answer within its bound? Checked once,
	// in the background — the page works offline, so a server that is down
	// must not delay it — and a failure is reported as
	// `aucom.readiness_failed`. No server chosen is not a failure; the page
	// asks for one.
	if address, err := settings.AUB(); err == nil {
		if client, err := aub.New(address, nil); err == nil {
			reporter := env.incidents(settings)
			go func() {
				if _, err := incident.CheckReadiness(ctx, client, aub.ReadinessBound, reporter, ""); err != nil && ctx.Err() == nil {
					logf("warning: the Auto-Pigeon server did not pass its readiness check; the page works offline until it answers")
				}
			}()
		}
	}
	url = web.URL(listener)
	if *interactive {
		logf("companion %s listening on %s", env.Version, url)
	} else {
		fmt.Fprintf(env.Stdout, "companion %s listening on %s\n", env.Version, url)
	}
	logf("API token written to %s", tokenPath)
	// The address this run actually bound, beside the token, so a clicked
	// `autopigeon://` link raises THIS page instead of starting a second server.
	urlPath := web.URLPath(configDir)
	if err := web.WriteURL(urlPath, url); err != nil {
		logf("warning: %v", err)
	}
	defer web.RemoveURL(urlPath)
	_ = joinintent.Prune(joinintent.Path(configDir), time.Now().UTC())

	opened := false
	if *open {
		opener := env.OpenBrowser
		if opener == nil {
			opener = web.OpenBrowser
		}
		// Never fatal: the URL is printed, and a machine with no browser
		// handler should still be able to use the server.
		page := url
		if *openArea == "games" {
			page = url + "#games"
		}
		if err := opener(page); err != nil {
			logf("warning: %v", err)
			if !*interactive {
				fmt.Fprintf(env.Stderr, "warning: %v\n", err)
				fmt.Fprintf(env.Stderr, "open %s manually\n", url)
			}
		} else {
			opened = true
		}
	}

	if *interactive {
		switch {
		case opened:
			fmt.Fprintf(env.Stdout, "Auto-Pigeon Companion %s is open in your browser.\n", env.Version)
			fmt.Fprintln(env.Stdout, "Close its last window to stop it, or press Ctrl+C.")
			// A browser command that "succeeded" is not a window: `xdg-open`
			// and `start` return before anything appears, and a browser that
			// joined an existing process leaves no handle to watch. The lease
			// is the authority, so if none has arrived, say where the page is.
			go func() {
				select {
				case <-time.After(noPageHint):
					if !lifecycle.EverConnected() {
						fmt.Fprintf(env.Stdout, "No page has opened yet. If your browser did not open it, go to %s\n", url)
					}
				case <-lifecycle.Done():
				}
			}()
		case *open:
			fmt.Fprintf(env.Stdout, "Auto-Pigeon Companion %s could not open your browser.\n", env.Version)
			fmt.Fprintf(env.Stdout, "Open %s within %s. Close its last window to stop it, or press Ctrl+C.\n",
				url, *startupWindow)
		default:
			fmt.Fprintf(env.Stdout, "Auto-Pigeon Companion %s is running at %s\n", env.Version, url)
			fmt.Fprintf(env.Stdout, "Open it within %s. Close its last window to stop it, or press Ctrl+C.\n", *startupWindow)
		}
	}

	// The lifecycle decides; web.Serve stops when it has.
	go lifecycle.Run(ctx, server.ActiveWork)
	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	go func() {
		select {
		case <-lifecycle.Done():
			stopServing()
		case <-serveCtx.Done():
		}
	}()

	serveErr := web.Serve(serveCtx, listener, server)
	// An interrupt that arrived while nothing else had decided.
	lifecycle.Stop(web.ExitInterrupted)
	cause := lifecycle.Cause()
	logf("lifecycle: stopping, cause %s", cause)
	if serveErr != nil {
		return fail(env, serveErr)
	}
	if *interactive {
		// Printed after the deferred shutdown work, so "stopped" is true when
		// it is read. Registered last, so it runs first among the defers —
		// hence the explicit Close calls here, which the defers then repeat
		// harmlessly.
		server.Close()
		service.Close()
		fmt.Fprintln(env.Stdout, web.ExitLine(cause, *startupWindow))
		if detailPath != "" && cause == web.ExitStartupTimeout {
			fmt.Fprintf(env.Stdout, "Details: %s\n", detailPath)
		}
		return 0
	}
	if cause == web.ExitQuit {
		fmt.Fprintln(env.Stdout, "stopped: Quit was chosen in the page")
		return 0
	}
	fmt.Fprintln(env.Stdout, "stopped")
	return 0
}

func runAuth(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(env.Stderr, "error: auth requires a subcommand: login, status, or logout")
		return 2
	}
	switch args[0] {
	case "login":
		return runAuthLogin(env, args[1:])
	case "status":
		return runAuthStatus(env, args[1:])
	case "logout":
		return runAuthLogout(env, args[1:])
	default:
		fmt.Fprintf(env.Stderr, "error: unknown auth subcommand %q (want login, status, or logout)\n", args[0])
		return 2
	}
}

func runAuthLogin(env *Env, args []string) int {
	set := newFlagSet(env, "auth login")
	email := set.String("email", "", "AUB account email")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	if *email == "" {
		fmt.Fprintln(env.Stderr, "error: auth login requires --email")
		return 2
	}

	password, code, ok := readPassword(env)
	if !ok {
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

	ctx, stop := signalContext()
	defer stop()

	session, err := client.Login(ctx, *email, password)
	if err != nil {
		return fail(env, err)
	}

	if _, err := updateSettings(env, func(current *config.Config) error {
		current.Session = config.Session{
			Token:   session.Token,
			UserID:  session.UserID,
			Email:   session.Email,
			Expires: session.Expires,
		}
		return nil
	}); err != nil {
		return fail(env, err)
	}
	fmt.Fprintf(env.Stdout, "signed in to %s as %s\n", client.BaseURL(), session.Email)
	return 0
}

// readPassword takes the password from AUL_PASSWORD, or reads one line from
// stdin. The int is the exit code to return when ok is false.
func readPassword(env *Env) (string, int, bool) {
	if value, ok := env.lookenv(PasswordEnv); ok && value != "" {
		return value, 0, true
	}
	if value, ok := env.lookenv(LegacyPasswordEnv); ok && value != "" {
		fmt.Fprintf(env.Stderr, "warning: %s is the retired Launcher's name for %s; set %s instead\n",
			LegacyPasswordEnv, PasswordEnv, PasswordEnv)
		return value, 0, true
	}
	if env.Stdin == nil {
		fmt.Fprintf(env.Stderr, "error: no password: set %s or pipe one on stdin\n", PasswordEnv)
		return "", 2, false
	}

	fmt.Fprint(env.Stderr, "password (visible): ")
	reader := bufio.NewReader(env.Stdin)
	line, err := reader.ReadString('\n')
	// io.EOF with content is a piped password without a trailing newline, which
	// is the normal shape of `printf %s "$p" | companion auth login`.
	if err != nil && line == "" {
		fmt.Fprintf(env.Stderr, "error: reading the password: %v\n", err)
		return "", 2, false
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		fmt.Fprintln(env.Stderr, "error: password is empty")
		return "", 2, false
	}
	return password, 0, true
}

func runAuthStatus(env *Env, args []string) int {
	set := newFlagSet(env, "auth status")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}

	baseURL, err := settings.AUB()
	if err != nil {
		// Not a failure of `auth status` — the question "am I signed in" still
		// has an answer — so this reports the missing address and continues.
		fmt.Fprintf(env.Stdout, "aub: (not configured — set %s or aub_base_url)\n", config.EnvAUBBaseURL)
	} else {
		fmt.Fprintf(env.Stdout, "aub: %s\n", baseURL)
	}
	if !settings.Session.Valid() {
		fmt.Fprintln(env.Stdout, "signed in: no")
		return 0
	}
	if aub.TokenExpired(settings.Session.Token, time.Now()) {
		fmt.Fprintf(env.Stdout, "signed in: no — the session for %s has expired; run `companion auth login`\n",
			settings.Session.Email)
		return 0
	}
	fmt.Fprintf(env.Stdout, "signed in: yes (%s)\n", settings.Session.Email)
	if !settings.Session.Expires.IsZero() {
		fmt.Fprintf(env.Stdout, "expires: %s (local estimate)\n", settings.Session.Expires.Format("2006-01-02 15:04"))
	}
	return 0
}

func runAuthLogout(env *Env, args []string) int {
	set := newFlagSet(env, "auth logout")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	if _, err := updateSettings(env, func(current *config.Config) error {
		current.Session = config.Session{}
		return nil
	}); err != nil {
		return fail(env, err)
	}
	// Said explicitly because it is not revocation: AUB's tokens are stateless
	// and stay valid until they expire. See aub.Client.Logout.
	fmt.Fprintln(env.Stdout, "signed out locally (the token stays valid at AUB until it expires)")
	return 0
}

// runLaunch is the retired `companion launch`.
//
// It read a per-game launch configuration that was never more than a
// placeholder (`internal/launch`'s example provider: a quakespasm path invented
// under a game root) and turned it into a generated engine profile. The curated
// engine profiles replaced that model in AUCOM 209, and NEW_244D removed the
// bridge: a control that started whatever the placeholder guessed was a second
// launch route beside the real one, and the one a first-time user was most
// likely to find. The command stays in the dispatcher only to say where the
// real route is, and it refuses with exit 2 so a script that still calls it
// cannot read a success.
func runLaunch(env *Env, args []string) int {
	fmt.Fprintln(env.Stderr, "error: `companion launch` is retired: it read a placeholder launch configuration, not your engine.")
	fmt.Fprintln(env.Stderr, "Set up the engine you have and start it through its profile instead:")
	fmt.Fprintln(env.Stderr, "  companion engine list")
	fmt.Fprintln(env.Stderr, "  companion engine bind <profile> --engine <path> --game-root <dir>")
	fmt.Fprintln(env.Stderr, "  companion engine run <profile> --action play_map --map <name>")
	return 2
}

// runMigrate runs the configuration migration on demand and prints its full
// report.
//
// The same migration runs automatically before every other command, so this
// exists for the two cases where automatic is not enough: checking what would
// be carried over before trusting it, and seeing the conflict list again after
// the first run scrolled past.
func runMigrate(env *Env, args []string) int {
	set := newFlagSet(env, "migrate")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	if env.ConfigPath != "" {
		fmt.Fprintln(env.Stderr, "error: migrate operates on the standard config location, not an explicit path")
		return 2
	}
	report, err := config.Migrate()
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprint(env.Stdout, report.Summary())
	if !strings.HasSuffix(report.Summary(), "\n") {
		fmt.Fprintln(env.Stdout)
	}
	return 0
}
