package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/aue"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/web"
)

func runVersion(env *Env, args []string) int {
	set := newFlagSet(env, "version")
	if _, err := parseInterspersed(set, args); err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 2
	}
	fmt.Fprintln(env.Stdout, env.Version)
	return 0
}

// runServe starts the local GUI server. It is also what an argument-less
// invocation dispatches to, which is why --no-browser exists: the same command
// has to serve both the double-click case and a scripted one.
func runServe(env *Env, args []string) int {
	set := newFlagSet(env, "serve")
	addr := set.String("addr", "", "listen address (default from config: "+config.DefaultServerAddr+")")
	noBrowser := set.Bool("no-browser", false, "do not open the default browser")
	rest, err := parseInterspersed(set, args)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 2
	}
	if len(rest) > 0 {
		fmt.Fprintf(env.Stderr, "error: serve takes no positional arguments, got %q\n", rest[0])
		return 2
	}

	settings, err := config.Load()
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	listenAddr := settings.ServerAddr
	if *addr != "" {
		listenAddr = *addr
	}

	runner := aue.NewEmbeddedRunner()
	defer runner.Close()

	server, err := web.Listen(web.Options{
		Addr:    listenAddr,
		Config:  settings,
		AUE:     runner,
		Version: env.Version,
		Logf:    func(format string, args ...any) { fmt.Fprintf(env.Stderr, format+"\n", args...) },
	})
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}

	// Printed before opening the browser so the URL is available even when
	// the browser fails to launch — on a headless machine that line is the
	// only way in.
	fmt.Fprintf(env.Stdout, "companion is serving %s\n", server.URL())

	if !*noBrowser {
		if err := web.OpenBrowser(server.URL()); err != nil {
			// Not fatal: the server is up and the URL is printed above.
			fmt.Fprintf(env.Stderr, "warning: %v\n", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Serve(ctx); err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// runAuth is a small second-level dispatcher. Auth has three closely related
// operations that share config loading and an AUB client, so they live under
// one command rather than as three top-level ones.
func runAuth(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, authUsage())
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(env.Stdout, authUsage())
		return 0
	case "login":
		return runAuthLogin(env, args[1:])
	case "status":
		return runAuthStatus(env, args[1:])
	case "logout":
		return runAuthLogout(env, args[1:])
	default:
		fmt.Fprintf(env.Stderr, "error: unknown auth subcommand %q\n\n", args[0])
		fmt.Fprint(env.Stderr, authUsage())
		return 2
	}
}

func authUsage() string {
	var builder strings.Builder
	builder.WriteString("usage:\n  companion auth login [--email <address>] [--password <password>]\n")
	builder.WriteString("  companion auth status\n  companion auth logout\n\n")
	builder.WriteString("Without --password, the password is read from stdin so it stays out of\nthe shell history and the process table.\n")
	return builder.String()
}

func runAuthLogin(env *Env, args []string) int {
	set := newFlagSet(env, "auth login")
	email := set.String("email", "", "AUB account email")
	password := set.String("password", "", "AUB account password (omit to read from stdin)")
	rest, err := parseInterspersed(set, args)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 2
	}
	if len(rest) > 0 {
		fmt.Fprintf(env.Stderr, "error: auth login takes no positional arguments, got %q\n", rest[0])
		return 2
	}
	if *email == "" {
		fmt.Fprintln(env.Stderr, "error: auth login requires --email")
		return 2
	}

	secret := *password
	if secret == "" {
		// No terminal-echo suppression: that needs golang.org/x/term or a
		// syscall dance per platform, and this repository is dependency-free.
		// Reading a piped secret is the documented path
		// (`printf %s "$PASSWORD" | companion auth login --email ...`); an
		// interactive user typing here will see what they type.
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			fmt.Fprintln(env.Stderr, "error: no password supplied on stdin")
			return 2
		}
		secret = strings.TrimRight(scanner.Text(), "\r\n")
	}
	if secret == "" {
		fmt.Fprintln(env.Stderr, "error: the password is empty")
		return 2
	}

	settings, err := config.Load()
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}

	client := aub.New(settings.AUBBaseURL)
	session, err := client.Login(context.Background(), *email, secret)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}

	settings.Session = config.Session{
		Token:      session.Token,
		UserID:     session.UserID,
		Email:      session.Email,
		ObtainedAt: session.ObtainedAt,
	}
	if err := config.Save(settings); err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}

	identity := session.Email
	if identity == "" {
		identity = *email
	}
	fmt.Fprintf(env.Stdout, "signed in as %s\n", identity)
	return 0
}

func runAuthStatus(env *Env, args []string) int {
	set := newFlagSet(env, "auth status")
	if _, err := parseInterspersed(set, args); err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 2
	}
	settings, err := config.Load()
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	if !settings.Session.Valid() {
		fmt.Fprintf(env.Stdout, "not signed in (%s)\n", settings.AUBBaseURL)
		return 0
	}
	identity := settings.Session.Email
	if identity == "" {
		identity = settings.Session.UserID
	}
	fmt.Fprintf(env.Stdout, "signed in as %s (%s)\n", identity, settings.AUBBaseURL)
	return 0
}

func runAuthLogout(env *Env, args []string) int {
	set := newFlagSet(env, "auth logout")
	if _, err := parseInterspersed(set, args); err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 2
	}
	settings, err := config.Load()
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	settings.Session = config.Session{}
	if err := config.Save(settings); err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	// Pocketbase record tokens are stateless, so this only forgets the local
	// copy — see aub.Client.Logout.
	fmt.Fprintln(env.Stdout, "signed out locally")
	return 0
}
