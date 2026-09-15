package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// An optional `.env` for development and local stacks.
//
// # What it is for
//
// A person testing the Companion against a local auto-pigeon-backend wants to
// say where that backend is once, beside the checkout, rather than exporting a
// variable in every terminal or writing it into the per-user config.json that
// their everyday install also reads. The workspace rule is that a component's
// address lives in `.env` and never in code; this is the Companion honouring
// the first half of that rule without giving up the second.
//
// # What it deliberately is not
//
//   - **Not required, and not read by default from anywhere a release lives.**
//     It is read from the working directory the command was started in, or from
//     the file AUCOM_ENV_FILE names. A clean machine has neither, and nothing
//     here changes when neither exists.
//   - **Not a way to set anything.** Only the keys in [DevEnvKeys] are honoured.
//     A `.env` sitting in whatever directory a terminal happened to be in must
//     not be able to swap the catalogue's trust anchors, point the extractor at
//     an unverified binary or relocate the job store; every other key is
//     reported as ignored, by name, and never applied.
//   - **Not stronger than the real environment.** A variable already set in the
//     process wins, so `AUCOM_AUB_BASE_URL=… companion` still means what it
//     says.
//   - **Not shell.** Nothing is expanded, sourced or executed. `KEY=VALUE`, an
//     optional `export `, optional matching quotes, and `#` comments.

// EnvFileVariable names a `.env` file to read instead of `./.env`.
const EnvFileVariable = "AUCOM_ENV_FILE"

// DefaultEnvFile is the file read from the working directory.
const DefaultEnvFile = ".env"

// DevEnvKeys are the only keys a `.env` file may supply.
var DevEnvKeys = []string{EnvAUBBaseURL}

// EnvFileReport says what a `.env` read did.
type EnvFileReport struct {
	// Path is the file that was read, or "" when there was none.
	Path string
	// Applied are the keys set from the file.
	Applied []string
	// Shadowed are allowed keys the file named that the environment already set.
	Shadowed []string
	// Ignored are keys the file named that a `.env` may not supply.
	Ignored []string
}

// LoadDevEnvFile reads the optional development `.env` and applies the keys it
// is allowed to supply. getenv and setenv are the process's own in production
// and fakes in a test.
//
// A missing default file is not an error. A file AUCOM_ENV_FILE names that does
// not exist is, because somebody asked for it by name.
func LoadDevEnvFile(getenv func(string) string, lookup func(string) (string, bool), setenv func(string, string) error) (EnvFileReport, error) {
	path := strings.TrimSpace(getenv(EnvFileVariable))
	named := path != ""
	if !named {
		path = DefaultEnvFile
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !named && errors.Is(err, fs.ErrNotExist) {
			return EnvFileReport{}, nil
		}
		return EnvFileReport{}, fmt.Errorf("config: reading %s: %w", path, err)
	}
	values, err := ParseEnvFile(raw)
	if err != nil {
		return EnvFileReport{}, fmt.Errorf("config: %s: %w", path, err)
	}
	// A `.env` that names no AUCOM_ key belongs to some other program — the
	// editor's own, when a terminal is sitting in that checkout — and is not
	// this program's to comment on. Found in NEW_244D's live session, where
	// running the Companion from AUP's directory printed a warning listing a
	// hundred of AUP's keys.
	ours := false
	for _, entry := range values {
		if strings.HasPrefix(entry.Key, "AUCOM_") {
			ours = true
			break
		}
	}
	if !ours && !named {
		return EnvFileReport{}, nil
	}
	report := EnvFileReport{Path: path}
	allowed := map[string]bool{}
	for _, key := range DevEnvKeys {
		allowed[key] = true
	}
	for _, entry := range values {
		if !allowed[entry.Key] {
			if strings.HasPrefix(entry.Key, "AUCOM_") {
				report.Ignored = append(report.Ignored, entry.Key)
			}
			continue
		}
		if _, set := lookup(entry.Key); set {
			report.Shadowed = append(report.Shadowed, entry.Key)
			continue
		}
		if err := setenv(entry.Key, entry.Value); err != nil {
			return report, fmt.Errorf("config: applying %s from %s: %w", entry.Key, path, err)
		}
		report.Applied = append(report.Applied, entry.Key)
	}
	return report, nil
}

// EnvEntry is one assignment in a `.env` file.
type EnvEntry struct {
	Key   string
	Value string
}

// ParseEnvFile reads `KEY=VALUE` lines. It expands nothing.
func ParseEnvFile(raw []byte) ([]EnvEntry, error) {
	var entries []EnvEntry
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	number := 0
	for scanner.Scan() {
		number++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" || strings.ContainsAny(key, " \t\"'") {
			return nil, fmt.Errorf("line %d is not KEY=VALUE", number)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		} else if index := strings.Index(value, " #"); index >= 0 {
			value = strings.TrimSpace(value[:index])
		}
		entries = append(entries, EnvEntry{Key: key, Value: value})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
