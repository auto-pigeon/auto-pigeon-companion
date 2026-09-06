package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aue"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/launch"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/tools"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/web"
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
const PasswordEnv = "AUL_PASSWORD"

// loadSettings reads the local config, tolerating a first run with no file.
func loadSettings(env *Env) (config.Config, error) {
	if env.ConfigPath != "" {
		settings, err := config.LoadFrom(env.ConfigPath)
		if errors.Is(err, config.ErrNotFound) {
			return settings, nil
		}
		return settings, err
	}
	settings, err := config.Load()
	if errors.Is(err, config.ErrNotFound) {
		return settings, nil
	}
	return settings, err
}

func saveSettings(env *Env, settings config.Config) error {
	if env.ConfigPath != "" {
		return config.SaveTo(env.ConfigPath, settings)
	}
	return config.Save(settings)
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

func runVersion(env *Env, args []string) int {
	set := newFlagSet(env, "version")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}
	fmt.Fprintln(env.Stdout, env.Version)
	return 0
}

// runServe starts the local GUI server. With --open it also opens the default
// browser, which is what the no-subcommand invocation does.
func runServe(env *Env, args []string) int {
	set := newFlagSet(env, "serve")
	port := set.Int("port", 0, "loopback port to bind; 0 uses the configured port")
	open := set.Bool("open", false, "open the page in the default browser")
	if _, code, ok := parseFlags(env, set, args); !ok {
		return code
	}

	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	chosen := *port
	if chosen == 0 {
		chosen = settings.Port
	}

	// The extractor runner is built here and closed when serve returns, so a
	// process that never touches AUE never extracts anything and a process
	// that does cleans up after itself.
	runner := aue.NewEmbeddedRunner()
	defer runner.Close()

	server, err := web.NewServer(web.Options{
		Version:    env.Version,
		Config:     settings,
		AUE:        runner,
		SaveConfig: func(updated config.Config) error { return saveSettings(env, updated) },
	})
	if err != nil {
		return fail(env, err)
	}

	listener, err := web.Listen(chosen)
	if err != nil {
		return fail(env, err)
	}
	url := web.URL(listener)
	fmt.Fprintf(env.Stdout, "auto-pigeon-launcher %s listening on %s\n", env.Version, url)

	if *open {
		opener := env.OpenBrowser
		if opener == nil {
			opener = web.OpenBrowser
		}
		// Never fatal: the URL is already printed, and a machine with no
		// browser handler should still be able to use the server.
		if err := opener(url); err != nil {
			fmt.Fprintf(env.Stderr, "warning: %v\n", err)
			fmt.Fprintf(env.Stderr, "open %s manually\n", url)
		}
	}

	ctx, stop := signalContext()
	defer stop()
	if err := web.Serve(ctx, listener, server); err != nil {
		return fail(env, err)
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

	settings.Session = config.Session{
		Token:   session.Token,
		UserID:  session.UserID,
		Email:   session.Email,
		Expires: session.Expires,
	}
	if err := saveSettings(env, settings); err != nil {
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
	if env.Stdin == nil {
		fmt.Fprintf(env.Stderr, "error: no password: set %s or pipe one on stdin\n", PasswordEnv)
		return "", 2, false
	}

	fmt.Fprint(env.Stderr, "password (visible): ")
	reader := bufio.NewReader(env.Stdin)
	line, err := reader.ReadString('\n')
	// io.EOF with content is a piped password without a trailing newline, which
	// is the normal shape of `printf %s "$p" | launcher auth login`.
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
	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	settings.Session = config.Session{}
	if err := saveSettings(env, settings); err != nil {
		return fail(env, err)
	}
	// Said explicitly because it is not revocation: AUB's tokens are stateless
	// and stay valid until they expire. See aub.Client.Logout.
	fmt.Fprintln(env.Stdout, "signed out locally (the token stays valid at AUB until it expires)")
	return 0
}

// runBuild runs one external tool through the full pipeline. Arguments after
// `--` go to the tool.
func runBuild(env *Env, args []string) int {
	set := newFlagSet(env, "build")
	tool := set.String("tool", tools.NoopToolName, "external tool to run")
	version := set.String("tool-version", "", "tool version; empty resolves the default")
	rest, code, ok := parseFlags(env, set, args)
	if !ok {
		return code
	}

	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}
	cache, err := settings.ToolCache()
	if err != nil {
		return fail(env, err)
	}

	// The fake tool until real ones are chosen. `--tool <anything else>` goes
	// to the real manager, which will report an unknown tool because the
	// registry is empty — the correct answer today, and the line that starts
	// working unchanged the moment the registry is populated.
	var manager tools.Manager
	if *tool == tools.NoopToolName {
		manager = tools.NewNoop(cache)
	} else {
		manager = tools.New(cache)
	}

	ctx, stop := signalContext()
	defer stop()

	request := tools.BuildRequest{Tool: *tool, Version: *version, Args: rest}
	if err := tools.Build(ctx, manager, request, env.Stdout, env.Stderr); err != nil {
		return fail(env, err)
	}
	return 0
}

func runLaunch(env *Env, args []string) int {
	set := newFlagSet(env, "launch")
	mapName := set.String("map", "", "map to load")
	gameRoot := set.String("game-root", "", "directory the game is installed in")
	dryRun := set.Bool("dry-run", false, "print the resolved command without starting it")
	rest, code, ok := parseInterspersed(env, set, args)
	if !ok {
		return code
	}
	if len(rest) == 0 {
		fmt.Fprintln(env.Stderr, "error: launch requires a game name")
		return 2
	}
	if len(rest) > 1 {
		fmt.Fprintf(env.Stderr, "error: launch accepts one game name, got %d\n", len(rest))
		return 2
	}

	settings, err := loadSettings(env)
	if err != nil {
		return fail(env, err)
	}

	ctx, stop := signalContext()
	defer stop()

	// The stubbed provider until AUB's schema is confirmed — see
	// internal/launch/config.go.
	configs, err := launch.ExampleProvider().Configs(ctx)
	if err != nil {
		return fail(env, err)
	}
	selected, err := launch.Find(configs, rest[0])
	if err != nil {
		return fail(env, err)
	}

	root := *gameRoot
	if root == "" {
		root = settings.GameRoots[selected.Game]
	}
	plan, err := launch.Resolve(launch.Request{Config: selected, GameRoot: root, Map: *mapName})
	if err != nil {
		return fail(env, err)
	}

	if *dryRun {
		fmt.Fprintln(env.Stdout, plan.String())
		return 0
	}
	if err := launch.Run(ctx, plan, env.Stdout, env.Stderr); err != nil {
		return fail(env, err)
	}
	return 0
}

// runExtractor reaches the bundled AUE binary from the command line.
//
// The GUI has the same capability on GET /api/aue/version. Both go through
// internal/aue rather than spawning a process themselves, and both are
// deliberately narrow: one named operation per subcommand, never a
// "run any AUE subcommand" pass-through, so every input a user can reach AUE
// with stays something this repository validated.
func runExtractor(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(env.Stderr, "error: extractor requires a subcommand: version")
		return 2
	}
	if args[0] != "version" {
		fmt.Fprintf(env.Stderr, "error: unknown extractor subcommand %q (want version)\n", args[0])
		return 2
	}
	set := newFlagSet(env, "extractor version")
	if _, code, ok := parseFlags(env, set, args[1:]); !ok {
		return code
	}

	runner := aue.NewEmbeddedRunner()
	defer runner.Close()

	ctx, stop := signalContext()
	defer stop()

	stdout, err := runner.Run(ctx, "version")
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintln(env.Stdout, strings.TrimSpace(string(stdout)))
	return 0
}
