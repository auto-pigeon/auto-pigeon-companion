package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/config"
)

// The Settings area.
//
// # Why the page can change these at all
//
// Everything else in this API is about doing work. This is about where the work
// happens: which backend, which port, where the caches are. A desktop program
// whose only way to set its backend address is a text editor and a restart is a
// program most of its users cannot set up at all — and the repository rule that
// no address may be compiled in (see AGENTS.md) means an unconfigured Companion
// has nothing to fall back on. So the first-run path has to be able to fix it,
// and Settings is where it does.
//
// # What it deliberately cannot change
//
// The directories are reported and never written. They come from the config
// file and the OS's own conventions, a running job has files open under them,
// and a page that could move the job store out from under the executor would be
// a page that could lose somebody's build. Changing one is a config-file edit
// followed by a restart, and the page says so rather than offering a control
// that would half-work.
//
// The session is not here either: it is written by signing in and cleared by
// signing out, and both of those are their own routes with their own meanings.

type settingsBody struct {
	// AUBBaseURL is what the configuration file holds. It is what a Save
	// writes back, so pressing Save never quietly copies an environment
	// variable's value into the file the user did not type it into.
	AUBBaseURL     string            `json:"aub_base_url"`
	Port           int               `json:"port"`
	JobConcurrency int               `json:"job_concurrency"`
	GameRoots      map[string]string `json:"game_roots,omitempty"`
	// Language is reported here and written only by its own route, so a Save
	// of the form never changes the page's language.
	Language string `json:"language,omitempty"`

	// Everything below is reported and never accepted. Marshalled into the
	// same object because a settings page needs to show a user where their
	// files are, and a second route for "the read-only half" would be a second
	// thing to keep in step.
	// AUBEffectiveURL is the address actually in use, which differs from the
	// stored one exactly when AUCOM_AUB_BASE_URL is set. Two members rather
	// than one, because "what is saved" and "what is running" are different
	// facts and a field that showed only the second would make a Save write it.
	AUBEffectiveURL    string `json:"aub_effective_url,omitempty"`
	AUBFromEnvironment bool   `json:"aub_from_environment,omitempty"`
	// Debug and Backends mirror /api/status, so Settings can draw its chooser
	// from one response.
	Debug              bool             `json:"debug"`
	Backends           []config.Backend `json:"backends,omitempty"`
	ConfigPath         string           `json:"config_path,omitempty"`
	ToolCacheDir       string           `json:"tool_cache_dir,omitempty"`
	JobsDir            string           `json:"jobs_dir,omitempty"`
	ProfilesDir        string           `json:"profiles_dir,omitempty"`
	BuildsDir          string           `json:"builds_dir,omitempty"`
	AssetCacheDir      string           `json:"asset_cache_dir,omitempty"`
	BindingsPath       string           `json:"bindings_path,omitempty"`
	CatalogBaseURL     string           `json:"catalog_url,omitempty"`
	CatalogAnchorsPath string           `json:"catalog_anchors_path,omitempty"`
	Offline            bool             `json:"offline"`
	// PathHelper is the native file chooser this machine has, or "" when it
	// has none and the page must fall back to a text field.
	PathHelper string `json:"path_helper,omitempty"`
}

func (s *Server) settingsAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /api/v1/settings": s.handleSettingsGet,
		"PUT /api/v1/settings": s.handleSettingsPut,
		"PUT /api/v1/settings/language": s.handleLanguagePut,
	}
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.describeSettings())
}

func (s *Server) describeSettings() settingsBody {
	settings := s.config()
	body := settingsBody{
		AUBBaseURL:         settings.AUBBaseURL,
		Port:               settings.Port,
		JobConcurrency:     settings.JobConcurrency,
		GameRoots:          settings.GameRoots,
		Language:           settings.Language,
		CatalogBaseURL:     settings.CatalogBaseURL,
		CatalogAnchorsPath: settings.CatalogAnchorsPath,
		Offline:            config.Offline(),
		PathHelper:         s.picker.Available(),
		Debug:              s.debug,
		Backends:           config.OfficialBackends,
	}
	// The environment wins over the file — see config.EnvAUBBaseURL — so a user
	// editing the field has to be told when what they type will not take
	// effect. Saying it here is the difference between a setting that appears
	// not to save and a setting the page explained.
	if _, fromEnv := os.LookupEnv(config.EnvAUBBaseURL); fromEnv {
		body.AUBFromEnvironment = true
	}
	if effective, err := settings.AUB(); err == nil {
		body.AUBEffectiveURL = effective
	}
	if path, err := config.Path(); err == nil {
		body.ConfigPath = path
	}
	if dir, err := settings.ToolCache(); err == nil {
		body.ToolCacheDir = dir
	}
	if dir, err := s.jobsDir(); err == nil {
		body.JobsDir = dir
	}
	if dir, err := s.profilesDir(); err == nil {
		body.ProfilesDir = dir
	}
	if dir, err := s.buildsDir(); err == nil {
		body.BuildsDir = dir
	}
	if dir, err := s.assetCacheDir(); err == nil {
		body.AssetCacheDir = dir
	}
	if path, err := s.bindingsPath(); err == nil {
		body.BindingsPath = path
	}
	return body
}

// handleSettingsPut writes the four fields a user may change.
//
// Whole-object, not a patch: the page always sends what it is showing, so a
// field left out is a field the caller does not have rather than one it wants
// cleared. Every value is validated before anything is written, and the client
// is rebuilt only after the file is saved — a settings change that failed to
// persist must not leave a running server pointed somewhere the next start
// would not go.
func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var request settingsBody
	if !decodeJSON(w, r, &request) {
		return
	}

	baseURL := strings.TrimSpace(request.AUBBaseURL)
	if baseURL != "" {
		if err := checkBackendURL(baseURL); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		// A person chooses one of the official deployments; typing any other
		// address is a developer's act and needs `--debug` (HITL, NEW_244D).
		// An address already saved is carried by a Save of the other fields,
		// so a value set in a debug session does not make the page unsavable.
		if !s.debug && !config.IsOfficialBackend(baseURL) && baseURL != strings.TrimSpace(s.config().AUBBaseURL) {
			writeError(w, http.StatusForbidden, errors.New(
				"only Auto-Pigeon or Auto-Pigeon beta can be chosen here; another server address needs the Companion started with --debug"))
			return
		}
		baseURL = strings.TrimRight(baseURL, "/")
	}
	if request.Port < 0 || request.Port > 65535 {
		writeError(w, http.StatusBadRequest,
			fmt.Errorf("%d is not a port; use 0 to let the operating system pick one", request.Port))
		return
	}
	if request.JobConcurrency < 0 {
		writeError(w, http.StatusBadRequest,
			errors.New("job concurrency cannot be negative; use 0 to let the executor choose"))
		return
	}
	roots, err := checkGameRoots(request.GameRoots)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	// Only the four fields this form owns. Everything else in the file is
	// carried forward from whatever is on disk when the lock is taken, so a
	// settings save cannot undo a catalogue address or a session another
	// instance wrote while this page was open.
	updated, err := s.updateConfig(func(current *config.Config) error {
		current.AUBBaseURL = baseURL
		current.Port = request.Port
		current.JobConcurrency = request.JobConcurrency
		current.GameRoots = roots
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Rebuilt around the saved address, carrying the session that is already
	// signed in. A different backend gets the same token, which it will reject
	// — and being told "that backend does not know this session" is the
	// accurate thing to be told after pointing at a different backend.
	client := s.aubClient()
	effective := updated.AUBBaseURL
	if fromEnv, err := updated.AUB(); err == nil {
		effective = fromEnv
	}
	if effective == "" {
		client = nil
	} else if client == nil || client.BaseURL() != effective {
		rebuilt, err := s.newAUB(effective)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		rebuilt.SetToken(updated.Session.Token)
		client = rebuilt
	}

	s.mu.Lock()
	s.settings = updated
	s.client = client
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, s.describeSettings())
}

// checkBackendURL refuses an address that would not work, with the reason.
//
// It is not a reachability check: a backend that is down is still the right
// address, and a settings page that refused to save one would be a settings
// page nobody could use to prepare a machine. What it refuses is a value that
// cannot be a backend at all — no scheme, a scheme no HTTP client speaks, no
// host — because those are the mistakes a person actually makes here, and each
// produces a much worse error later.
func checkBackendURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q is not a URL: %w", raw, err)
	}
	switch parsed.Scheme {
	case "http", "https":
	case "":
		return fmt.Errorf("%q has no scheme; write it as http://host:port or https://host", raw)
	default:
		return fmt.Errorf("%q uses the %q scheme; auto-pigeon-backend is reached over http or https",
			raw, parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%q names no host", raw)
	}
	return nil
}

// checkGameRoots validates each configured game directory.
//
// A game root that is not there is a setting that produces a confusing failure
// at launch time rather than here, so it is refused now, named, with the game
// it belongs to. An empty map and a nil map both mean "none configured".
func checkGameRoots(roots map[string]string) (map[string]string, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	checked := make(map[string]string, len(roots))
	for game, dir := range roots {
		game = strings.TrimSpace(game)
		if game == "" {
			return nil, errors.New("a game root was given with no game name")
		}
		if strings.TrimSpace(dir) == "" {
			// An explicitly emptied field means "forget this one", which is
			// how a user removes a root they no longer have.
			continue
		}
		resolved, err := checkDirectory(dir)
		if err != nil {
			return nil, fmt.Errorf("the game directory for %s: %w", game, err)
		}
		checked[game] = resolved
	}
	if len(checked) == 0 {
		return nil, nil
	}
	return checked, nil
}

// backendLabel names an official deployment, or "" for any other address.
func backendLabel(address string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(address), "/")
	for _, backend := range config.OfficialBackends {
		if trimmed == backend.URL {
			return backend.Label
		}
	}
	return ""
}

// pageLanguages are the language codes the page offers (assets/i18n.js).
// "" is automatic.
var pageLanguages = map[string]bool{"": true, "en": true, "it": true, "fr": true, "de": true, "es": true, "ja": true, "zh": true}

// handleLanguagePut records the page's language. It is its own route because
// it is chosen from the header, not from the Settings form, and a form Save
// must not be able to undo it.
func (s *Server) handleLanguagePut(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Language string `json:"language"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	language := strings.TrimSpace(request.Language)
	if !pageLanguages[language] {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%q is not a language this page offers", language))
		return
	}
	if _, err := s.updateConfig(func(current *config.Config) error {
		current.Language = language
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.describeSettings())
}
