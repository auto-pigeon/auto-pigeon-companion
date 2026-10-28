package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/web"
)

// `companion tools` — the managed toolchains from a terminal (NEW_367). Like
// Auto-build, the coordinator lives inside the running Companion, so every
// subcommand is a request to it: the page and the terminal see one answer.

const toolsUsage = `usage:
  companion tools status [--json]                      what is installed, being set up or offered
  companion tools check [--json]                       check GitHub for new releases now
  companion tools retry [<tool>]                       try a failed setup again now
  companion tools update <tool> --now|--on-close       install the offered update
  companion tools skip <tool>                          cancel the offer; with --dont-ask-again, for this version
  companion tools cancel-pending <tool>                drop an update chosen but not yet active
  companion tools reset-skipped [<tool>]               offer skipped versions again
  companion tools use-managed <profile-id>             bind a profile you set up yourself to the managed installation

<tool> is ericw-tools or q3map2.
`

func runTools(env *Env, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, toolsUsage)
		return 2
	}
	verb, rest := args[0], args[1:]
	if verb == "-h" || verb == "--help" || verb == "help" {
		fmt.Fprint(env.Stdout, toolsUsage)
		return 0
	}
	set := newFlagSet(env, "tools "+verb)
	asJSON := set.Bool("json", false, "print the answer as JSON")
	now := set.Bool("now", false, "update: install now and switch once nothing uses the old version")
	onClose := set.Bool("on-close", false, "update: install in the background and switch when the Companion closes")
	dontAsk := set.Bool("dont-ask-again", false, "skip: do not offer this version again")
	positionals, code, ok := parseInterspersed(env, set, rest)
	if !ok {
		return code
	}
	one := func() (string, bool) {
		if len(positionals) != 1 {
			fmt.Fprintf(env.Stderr, "error: tools %s takes one name\n", verb)
			return "", false
		}
		return positionals[0], true
	}

	method, path, body := http.MethodGet, "/api/v1/tools", any(nil)
	switch verb {
	case "status":
	case "check":
		method, path, body = http.MethodPost, "/api/v1/tools/check", map[string]any{}
	case "retry":
		tool := "all"
		if len(positionals) == 1 {
			tool = positionals[0]
		}
		if tool == "all" {
			// Every tool: the server's Retry takes one, so ask for each.
			for _, t := range managedtools.Tools {
				if code := toolsRequest(env, http.MethodPost, "/api/v1/tools/"+string(t.ID)+"/retry", map[string]any{}, true, nil); code != 0 {
					return code
				}
			}
			return toolsRequest(env, http.MethodGet, "/api/v1/tools", nil, *asJSON, printToolsStatus)
		}
		method, path, body = http.MethodPost, "/api/v1/tools/"+url.PathEscape(tool)+"/retry", map[string]any{}
	case "update", "skip":
		tool, ok := one()
		if !ok {
			return 2
		}
		decision := "cancel"
		if verb == "update" {
			switch {
			case *now == *onClose:
				fmt.Fprintln(env.Stderr, "error: tools update needs exactly one of --now and --on-close")
				return 2
			case *now:
				decision = "now"
			default:
				decision = "on_exit"
			}
		}
		offer, code := currentOffer(env, tool)
		if code != 0 {
			return code
		}
		method, path = http.MethodPost, "/api/v1/tools/"+url.PathEscape(tool)+"/decide"
		body = map[string]any{"decision": decision, "version": offer.Version, "asset_id": offer.AssetID,
			"sha256": offer.SHA256, "dont_ask": *dontAsk && verb == "skip"}
	case "cancel-pending":
		tool, ok := one()
		if !ok {
			return 2
		}
		method, path, body = http.MethodPost, "/api/v1/tools/"+url.PathEscape(tool)+"/cancel-pending", map[string]any{}
	case "reset-skipped":
		request := map[string]any{}
		if len(positionals) == 1 {
			request["tool"] = positionals[0]
		}
		method, path, body = http.MethodPost, "/api/v1/tools/skipped/reset", request
	case "use-managed":
		id, ok := one()
		if !ok {
			return 2
		}
		return toolsRequest(env, http.MethodPost, "/api/v1/profiles/"+url.PathEscape(id)+"/use-managed", map[string]any{}, *asJSON,
			func(env *Env, answer map[string]any) {
				fmt.Fprintf(env.Stdout, "%s now uses the Companion's own installation.\n", id)
			})
	default:
		fmt.Fprintf(env.Stderr, "error: unknown tools subcommand %q\n\n", verb)
		fmt.Fprint(env.Stderr, toolsUsage)
		return 2
	}
	return toolsRequest(env, method, path, body, *asJSON, printToolsStatus)
}

// currentOffer is the bytes on offer for a tool, so a decision names them.
func currentOffer(env *Env, tool string) (managedtools.Candidate, int) {
	var offer *managedtools.Candidate
	code := toolsRequest(env, http.MethodGet, "/api/v1/tools", nil, true, nil, func(raw []byte) {
		var status managedtools.Status
		if json.Unmarshal(raw, &status) == nil {
			for _, t := range status.Tools {
				if string(t.ID) == tool {
					offer = t.Offer
				}
			}
		}
	})
	if code != 0 {
		return managedtools.Candidate{}, code
	}
	if offer == nil {
		fmt.Fprintf(env.Stderr, "error: no update of %s is on offer; see `companion tools status`\n", tool)
		return managedtools.Candidate{}, 1
	}
	return *offer, 0
}

func toolsRequest(env *Env, method, path string, body any, asJSON bool, print func(*Env, map[string]any), raw ...func([]byte)) int {
	dir, err := configDir(env)
	if err != nil {
		return fail(env, err)
	}
	address, running := runningServer(dir)
	if !running {
		fmt.Fprintln(env.Stderr, "error: no Auto-Pigeon Companion is running for this configuration; "+
			"build tools are set up and updated inside it — open the Companion and try again")
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
	response, err := (&http.Client{Timeout: 2 * time.Minute}).Do(request)
	if err != nil {
		return fail(env, err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	var answer map[string]any
	_ = json.Unmarshal(data, &answer)
	if response.StatusCode >= 300 {
		message, _ := answer["error"].(string)
		if message == "" {
			message = strings.TrimSpace(string(data))
		}
		fmt.Fprintf(env.Stderr, "error: %s\n", message)
		return 1
	}
	for _, r := range raw {
		r(data)
	}
	if print == nil {
		return 0
	}
	if asJSON {
		return printJSON(env, answer)
	}
	print(env, answer)
	return 0
}

// printToolsStatus is the status as a few lines per tool.
func printToolsStatus(env *Env, answer map[string]any) {
	if status, nested := answer["status"].(map[string]any); nested {
		for _, outcome := range asList(answer["outcomes"]) {
			if m, ok := outcome.(map[string]any); ok {
				fmt.Fprintf(env.Stdout, "check: %v\n", m["message"])
			}
		}
		answer = status
	}
	raw, _ := json.Marshal(answer)
	var status managedtools.Status
	if err := json.Unmarshal(raw, &status); err != nil {
		fmt.Fprintln(env.Stdout, string(raw))
		return
	}
	if available, _ := answer["available"].(bool); !available {
		fmt.Fprintln(env.Stdout, "Automatic build-tool setup is not running in this Companion.")
		return
	}
	fmt.Fprintf(env.Stdout, "Build tools for %s (%s channel), in %s\n", status.Target, status.Channel, status.Root)
	for _, tool := range status.Tools {
		line := fmt.Sprintf("  %-34s %s", tool.Title, tool.Phase)
		if tool.Version != "" {
			line += " " + tool.Version
		}
		if tool.Phase == managedtools.PhaseDownloading && tool.Total > 0 {
			line += fmt.Sprintf(" (%d%%)", tool.Done*100/tool.Total)
		}
		fmt.Fprintln(env.Stdout, line)
		if tool.Active != nil {
			fmt.Fprintf(env.Stdout, "      %s, %s, sha256 %s\n      %s\n", tool.Active.Tag, tool.Active.Banner, tool.Active.ActualSHA256, tool.Active.Dir)
		}
		if tool.Detail != "" {
			fmt.Fprintf(env.Stdout, "      %s\n", tool.Detail)
		}
		if tool.Offer != nil {
			fmt.Fprintf(env.Stdout, "      update offered: %s → %s (%s)\n      `companion tools update %s --now` or `--on-close`, or `companion tools skip %s`\n",
				tool.Version, tool.Offer.Version, tool.Offer.NotesURL, tool.ID, tool.ID)
		}
		if tool.Pending != nil {
			state := "downloading"
			if tool.Pending.Staged {
				state = "ready"
			}
			fmt.Fprintf(env.Stdout, "      update %s chosen (%s), %s", tool.Pending.Version, tool.Pending.Intent, state)
			if len(tool.Pending.Waiting) > 0 {
				fmt.Fprintf(env.Stdout, "; waiting for: %s", strings.Join(tool.Pending.Waiting, "; "))
			}
			fmt.Fprintln(env.Stdout)
		}
		if tool.Check.LastError != "" {
			fmt.Fprintf(env.Stdout, "      last check failed: %s\n", tool.Check.LastError)
		}
		for _, skipped := range tool.Skipped {
			fmt.Fprintf(env.Stdout, "      skipped: %s\n", skipped.Version)
		}
	}
}

func asList(value any) []any {
	list, _ := value.([]any)
	return list
}

// EnvManagedTools set to "off" keeps every Companion this environment starts
// from setting up or checking build tools, whatever it was started with: a
// test harness, a CI job, or a machine whose owner wants no downloads and
// cannot edit Settings first.
const EnvManagedTools = "AUCOM_MANAGED_TOOLS"

// managedToolsMode decides whether this `serve` runs the coordinator.
func managedToolsMode(flag string, interactive bool, settings config.Config) (on bool, why string, err error) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv(EnvManagedTools)), "off") {
		return false, EnvManagedTools + "=off", nil
	}
	switch flag {
	case "", "auto":
		if !interactive {
			return false, "server mode; start with --managed-tools on to set build tools up here", nil
		}
	case "on":
	case "off":
		return false, "--managed-tools off", nil
	default:
		return false, "", fmt.Errorf("--managed-tools is auto, on or off, not %q", flag)
	}
	if settings.ManagedToolsOff {
		return true, "turned off in Settings", nil
	}
	if config.Offline() {
		return true, config.EnvOffline + " forbids network access", nil
	}
	return true, "", nil
}

// newManagedTools builds the coordinator for a server. disabled keeps its
// status (and anything installed) while checking and installing nothing.
func newManagedTools(env *Env, settings config.Config, configDir string, server *web.Server, disabled bool, logf func(string, ...any)) (*managedtools.Manager, error) {
	root, err := settings.Tools()
	if err != nil {
		return nil, err
	}
	channel, err := managedtools.ParseChannel(settings.ToolsChannel)
	if err != nil {
		return nil, err
	}
	probes, err := managedtools.BuiltinProbes()
	if err != nil {
		return nil, err
	}
	source := managedtools.NewGitHubSource("auto-pigeon-companion/" + env.Version)
	if fixture, ok, err := fixtureReleaseSource(); err != nil {
		return nil, err
	} else if ok {
		logf("managed tools: TASK FIXTURE release source in use (%s); not GitHub", os.Getenv(fixtureReleasesEnv))
		source = fixture
	}
	return managedtools.New(managedtools.Options{
		ToolsRoot: root,
		StatePath: filepath.Join(configDir, managedtools.StateFileName),
		Source:    source,
		Channel:   channel,
		Host:      server.ManagedToolsHost(),
		Probes:    probes,
		Disabled:  disabled,
		Logf:      logf,
	})
}
