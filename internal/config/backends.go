package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Where a person's Auto-Pigeon account lives.
//
// # A HITL decision that narrows a workspace rule, and how far
//
// AGENTS.md forbids compiling in where another component lives, and the reason
// is a LAN: a loopback or 192.168 address compiled in turns a misconfiguration
// into a plausible wrong answer. On 2026-09-15 the project owner decided (in
// NEW_244D's session) that a person using a released Companion should not be
// typing a server address at all — Auto-Pigeon and its account service are the
// project's own deployments — and that the two public deployments may be
// OFFERED by name: https://auto-pigeon.com and https://beta.auto-pigeon.com.
//
// What that decision does not change:
//
//   - **Nothing is chosen for the person.** A first run has no address and
//     contacts neither deployment until somebody picks one (the owner's own
//     choice, over a preselected default), so a signed-out, offline, local-only
//     Companion stays network-free.
//   - **No loopback or LAN address is compiled in, ever.** A local or
//     development stack is still configuration: the environment, the optional
//     `.env` in the working directory, or the override file beside the
//     executable — and typing one into Settings needs `--debug`.

// Backend is one official Auto-Pigeon deployment a person may choose.
type Backend struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	URL   string `json:"url"`
}

// OfficialBackends are the deployments Settings and the sign-in dialog offer.
var OfficialBackends = []Backend{
	{ID: "auto-pigeon", Label: "Auto-Pigeon", URL: "https://auto-pigeon.com"},
	{ID: "auto-pigeon-beta", Label: "Auto-Pigeon beta", URL: "https://beta.auto-pigeon.com"},
}

// IsOfficialBackend reports whether an address is one of OfficialBackends,
// ignoring a trailing slash.
func IsOfficialBackend(address string) bool {
	trimmed := strings.TrimRight(strings.TrimSpace(address), "/")
	for _, backend := range OfficialBackends {
		if trimmed == backend.URL {
			return true
		}
	}
	return false
}

// EnvPort is the GUI port an override supplies when `--port` is not given.
const EnvPort = "AUCOM_PORT"

// OverrideFileName is the optional file beside the executable.
const OverrideFileName = "config.json"

// executableOverride is everything the file beside the executable may say.
// Decoded strictly: a key this build does not know is refused by name rather
// than ignored, because a person who wrote `catalog_url` there believes it did
// something.
type executableOverride struct {
	AUBBaseURL string `json:"aub_base_url,omitempty"`
	Port       int    `json:"port,omitempty"`
}

// LoadExecutableOverride reads `config.json` beside the running executable —
// the root of an unpacked release — and applies what it says as the process
// environment the rest of the program already reads. The real environment
// wins. A missing file is the ordinary case.
func LoadExecutableOverride(executable string, lookup func(string) (string, bool), setenv func(string, string) error) (EnvFileReport, error) {
	if executable == "" {
		return EnvFileReport{}, nil
	}
	path := filepath.Join(filepath.Dir(executable), OverrideFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return EnvFileReport{}, nil
		}
		return EnvFileReport{}, fmt.Errorf("config: reading %s: %w", path, err)
	}
	var override executableOverride
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&override); err != nil {
		return EnvFileReport{}, fmt.Errorf("config: %s may hold only aub_base_url and port: %w", path, err)
	}
	report := EnvFileReport{Path: path}
	apply := func(key, value string) error {
		if value == "" {
			return nil
		}
		if _, set := lookup(key); set {
			report.Shadowed = append(report.Shadowed, key)
			return nil
		}
		if err := setenv(key, value); err != nil {
			return err
		}
		report.Applied = append(report.Applied, key)
		return nil
	}
	if err := apply(EnvAUBBaseURL, strings.TrimSpace(override.AUBBaseURL)); err != nil {
		return report, err
	}
	if override.Port != 0 {
		if override.Port < 0 || override.Port > 65535 {
			return report, fmt.Errorf("config: %s: port %d is not a port", path, override.Port)
		}
		if err := apply(EnvPort, strconv.Itoa(override.Port)); err != nil {
			return report, err
		}
	}
	return report, nil
}

// PortOverride is the port the environment names, or 0.
func PortOverride() int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(EnvPort)))
	if err != nil || value < 0 || value > 65535 {
		return 0
	}
	return value
}
