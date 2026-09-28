package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/web"
)

// `companion autobuild` — Auto-build from a terminal (NEW_265).
//
// Auto-build lives inside a running Companion: its one poller and the builds
// it starts belong to `companion serve`, which is also what the page talks to.
// So this command is a client of that same local API, with the same token the
// page uses — not a second poller, and not a second way to start a build. When
// no Companion is running it says so, rather than pretending to switch on
// something nothing would carry out.

const autobuildUsage = `usage:
  companion autobuild list [--json]                           every map's auto-build
  companion autobuild show <map-id> [--json]
  companion autobuild on <map-id> --pipeline <id> [--name <map name>]
                                                              check it every 30 s; build each new revision
  companion autobuild off <map-id>                            stop checking; a running build is left to finish
  companion autobuild pipeline <map-id> --pipeline <id>       the build profile the next build uses
  companion autobuild build-now <map-id> [--pipeline <id>]    build the current revision now
  companion autobuild retry <map-id>                          build the revision whose build failed, once

Auto-build runs inside "companion serve"; start the Companion first.
`

func runAutobuild(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, autobuildUsage)
		return 2
	}
	verb, rest := args[0], args[1:]
	if verb == "-h" || verb == "--help" || verb == "help" {
		fmt.Fprint(env.Stdout, autobuildUsage)
		return 0
	}
	set := newFlagSet(env, "autobuild "+verb)
	asJSON := set.Bool("json", false, "print the answer as JSON")
	pipeline := set.String("pipeline", "", "a build profile id (see `companion build pipelines`)")
	name := set.String("name", "", "the map's name, for display")
	positionals, code, ok := parseInterspersed(env, set, rest)
	if !ok {
		return code
	}

	method, path, body := http.MethodGet, "/api/v1/autobuild", map[string]any(nil)
	switch verb {
	case "list":
	case "show", "on", "off", "pipeline", "build-now", "retry":
		if len(positionals) != 1 {
			fmt.Fprintf(env.Stderr, "error: autobuild %s takes one map id\n", verb)
			return 2
		}
		path += "/" + url.PathEscape(positionals[0])
		switch verb {
		case "on":
			if *pipeline == "" {
				fmt.Fprintln(env.Stderr, "error: autobuild on needs --pipeline")
				return 2
			}
			method, path, body = http.MethodPost, path+"/enable", map[string]any{"pipeline": *pipeline, "display_name": *name}
		case "off":
			method, path, body = http.MethodPost, path+"/disable", map[string]any{}
		case "pipeline":
			if *pipeline == "" {
				fmt.Fprintln(env.Stderr, "error: autobuild pipeline needs --pipeline")
				return 2
			}
			method, path, body = http.MethodPost, path+"/pipeline", map[string]any{"pipeline": *pipeline}
		case "build-now":
			method, path, body = http.MethodPost, path+"/build-now", map[string]any{"pipeline": *pipeline, "display_name": *name}
		case "retry":
			method, path, body = http.MethodPost, path+"/retry", map[string]any{}
		}
	default:
		fmt.Fprintf(env.Stderr, "error: unknown autobuild subcommand %q\n\n", verb)
		fmt.Fprint(env.Stderr, autobuildUsage)
		return 2
	}

	dir, err := configDir(env)
	if err != nil {
		return fail(env, err)
	}
	address, running := runningServer(dir)
	if !running {
		fmt.Fprintln(env.Stderr, "error: no Auto-Pigeon Companion is running for this configuration; "+
			"Auto-build runs inside it — start it with `companion serve` (or open the Companion) and try again")
		return 1
	}
	token, err := web.ReadToken(web.TokenPath(dir))
	if err != nil {
		return fail(env, err)
	}
	var payload io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, strings.TrimRight(address, "/")+path, payload)
	if err != nil {
		return fail(env, err)
	}
	request.Header.Set("X-AUCOM-Token", token)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: 60 * time.Second}).Do(request)
	if err != nil {
		return fail(env, err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	var answer map[string]any
	_ = json.Unmarshal(raw, &answer)
	if response.StatusCode >= 300 {
		message, _ := answer["error"].(string)
		if message == "" {
			message = strings.TrimSpace(string(raw))
		}
		fmt.Fprintf(env.Stderr, "error: %s\n", message)
		return 1
	}
	if *asJSON {
		return printJSON(env, answer)
	}
	if items, isList := answer["items"].([]any); isList {
		if len(items) == 0 {
			fmt.Fprintln(env.Stdout, "Auto-build is not switched on for any map.")
		}
		for _, item := range items {
			printAutobuild(env, item.(map[string]any))
		}
		return 0
	}
	printAutobuild(env, answer)
	return 0
}

func printAutobuild(env *Env, entry map[string]any) {
	revision := func(value any) string {
		if row, ok := value.(map[string]any); ok {
			return fmt.Sprintf("revision %v", row["revision"])
		}
		return "—"
	}
	state := "off"
	if entry["enabled"] == true {
		state = "on"
	}
	name, _ := entry["display_name"].(string)
	fmt.Fprintf(env.Stdout, "%s %s: auto-build %s, pipeline %v\n", entry["asset_id"], name, state, entry["pipeline_id"])
	if at, ok := entry["last_check_at"].(string); ok {
		fmt.Fprintf(env.Stdout, "  checked %s; on the server: %s\n", at, revision(entry["observed"]))
	}
	if entry["pending"] != nil {
		fmt.Fprintf(env.Stdout, "  waiting to build: %s\n", revision(entry["pending"]))
	}
	for _, key := range []string{"running", "last_built", "failed"} {
		attempt, ok := entry[key].(map[string]any)
		if !ok {
			continue
		}
		fmt.Fprintf(env.Stdout, "  %s: %s, run %v", strings.ReplaceAll(key, "_", " "), revision(attempt["revision"]), attempt["run_id"])
		if job, _ := attempt["job_id"].(string); job != "" {
			fmt.Fprintf(env.Stdout, ", job %s", job)
		}
		if why, _ := attempt["error"].(string); why != "" {
			fmt.Fprintf(env.Stdout, " — %s", why)
		}
		fmt.Fprintln(env.Stdout)
	}
	if message, _ := entry["check_error"].(string); message != "" {
		fmt.Fprintf(env.Stdout, "  %s\n", message)
	}
}
