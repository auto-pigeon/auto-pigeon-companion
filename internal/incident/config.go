package incident

import (
	"net/url"
	"regexp"
	"strings"
)

// The configuration the Companion reads. There is no default DSN, no fallback
// host and no compiled-in GlitchTip: a component's address lives in `.env` or
// configuration, never in code (workspace AGENTS.md).
const (
	// EnvDSN is where incidents go. Unset — the ordinary case, and every
	// released install on a person's machine — means incidents are built,
	// validated, redacted and written to this process's own log, and nothing
	// is sent anywhere.
	EnvDSN = "AUCOM_INCIDENT_DSN"
	// EnvEnvironment is which deployment an incident is filed under:
	// development, production or test.
	EnvEnvironment = "AUCOM_INCIDENT_ENVIRONMENT"
)

// Config is the resolved reporting configuration.
type Config struct {
	DSN         string
	Environment string
	// Release is the Companion's own `1.<commit-count>`, or `unknown` for a
	// build nobody stamped. Never invented.
	Release string
}

// Enabled reports whether anything is sent anywhere.
func (c Config) Enabled() bool { return c.DSN != "" }

var stampedRelease = regexp.MustCompile(`^1\.[0-9]+$`)

// LoadConfig resolves the configuration: the environment wins over the config
// file, as it does for the AUB address. It cannot fail — a bad value in an
// optional observability knob must not stop the Companion starting.
//
// An unset environment is `production` for a stamped release and `development`
// for an unstamped build: a label on a report, not a privilege.
func LoadConfig(lookup func(string) (string, bool), fileDSN, fileEnvironment, release string) Config {
	get := func(name string) string {
		if lookup == nil {
			return ""
		}
		value, _ := lookup(name)
		return strings.TrimSpace(value)
	}
	cfg := Config{DSN: get(EnvDSN), Release: strings.TrimSpace(release)}
	if cfg.DSN == "" {
		cfg.DSN = strings.TrimSpace(fileDSN)
	}
	if !stampedRelease.MatchString(cfg.Release) {
		cfg.Release = "unknown"
	}
	environment := strings.ToLower(get(EnvEnvironment))
	if environment == "" {
		environment = strings.ToLower(strings.TrimSpace(fileEnvironment))
	}
	switch environment {
	case EnvironmentDevelopment, EnvironmentProduction, EnvironmentTest:
		cfg.Environment = environment
	default:
		cfg.Environment = EnvironmentDevelopment
		if cfg.Release != "unknown" {
			cfg.Environment = EnvironmentProduction
		}
	}
	return cfg
}

// dsn is a parsed DSN: `scheme://<public key>@host[:port][/prefix]/<project>`.
type dsn struct {
	store string
	key   string
}

// parseDSN returns the store URL and key, or false. A malformed value is
// treated as absent rather than half-used: a transport that guessed at a broken
// address would send to somewhere nobody chose.
func parseDSN(raw string) (dsn, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return dsn{}, false
	}
	if parsed.User == nil || parsed.User.Username() == "" {
		return dsn{}, false
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	project := segments[len(segments)-1]
	if project == "" {
		return dsn{}, false
	}
	prefix := ""
	if len(segments) > 1 {
		prefix = "/" + strings.Join(segments[:len(segments)-1], "/")
	}
	return dsn{
		store: parsed.Scheme + "://" + parsed.Host + prefix + "/api/" + url.PathEscape(project) + "/store/",
		key:   parsed.User.Username(),
	}, true
}
