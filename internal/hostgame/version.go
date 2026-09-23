package hostgame

import (
	"strings"
	"unicode"
)

// ObservedEngineVersion reads the engine's version out of what the running
// engine printed as it started.
//
// A listing used to publish the engine profile's `engine_version`, which is the
// RANGE the profile supports ("1.30.x"), not the version that is running: a
// vkQuake 1.36.0 host was listed as "vkquake 1.30.x" (operator, 2026-09-23).
// The running engine says what it is in its own startup output —
// `Initializing vkQuake 1.36.0`, `QuakeSpasm 0.96.3`, `Ironwail 0.7.0` — and
// that output is already in the job's log, so reading it asks the process that
// is actually being joined, and runs nothing extra.
//
// The rule is literal: the first line where the runtime's name (any case) is
// followed by a version-shaped word — digits and dots, optionally a leading
// `v` and a suffix such as `-beta1`. Anything else is not an answer, and the
// caller keeps the profile's range rather than guessing.
func ObservedEngineVersion(runtime, output string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(runtime))
	if name == "" {
		return "", false
	}
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		for i := 0; i+1 < len(fields); i++ {
			if strings.ToLower(fields[i]) != name {
				continue
			}
			if version, ok := versionWord(fields[i+1]); ok {
				return version, true
			}
		}
	}
	return "", false
}

// versionWord accepts `1.36.0`, `v0.7.0`, `0.96.3-beta1` and refuses `1`,
// `Server` and `(59507`: at least two dot-separated numbers, then an optional
// suffix of letters, digits, `-`, `+` and `.`.
func versionWord(word string) (string, bool) {
	word = strings.TrimRight(word, ",;:)")
	word = strings.TrimPrefix(strings.TrimPrefix(word, "v"), "V")
	if len(word) > 64 {
		return "", false
	}
	// The numeric core runs to the first character that is neither a digit nor
	// a dot; an empty part (`1..2`, `.5`, `1.`) is not a version.
	end := strings.IndexFunc(word, func(c rune) bool { return !unicode.IsDigit(c) && c != '.' })
	if end < 0 {
		end = len(word)
	}
	core, suffix := word[:end], word[end:]
	parts := strings.Split(core, ".")
	if len(parts) < 2 {
		return "", false
	}
	for _, part := range parts {
		if part == "" {
			return "", false
		}
	}
	for _, c := range suffix {
		if !(unicode.IsLetter(c) || unicode.IsDigit(c) || c == '-' || c == '+' || c == '.') {
			return "", false
		}
	}
	return word, true
}
