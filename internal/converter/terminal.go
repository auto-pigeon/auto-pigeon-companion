package converter

import (
	"encoding/json"
	"strings"
)

// TerminalMarker is how a converter begins its last stderr line.
const TerminalMarker = "MAPIO-TERMINAL/1.0 "

// HistoricTerminalMarker is how the extractor a Companion used to run began the same line. Nothing
// writes it now; it is read so a build recorded before NEW_371 keeps its reason.
const HistoricTerminalMarker = "AUE-TERMINAL/1.0 "

// Terminal is the part of a terminal record a person is shown.
type Terminal struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Detail  string `json:"detail"`
}

// ReadTerminal finds a terminal record inside an error's text.
//
// It lives here because two surfaces read it — Build & Run's Activity card and,
// since `Q3_010`, every build that converts a map — and the converter's own
// refusal (`apmap-to-q3map refused [q3map_shader_unsafe]`) is in this record,
// after an exit code. A surface that printed the stderr whole showed the reason
// and made it unfindable in the same line.
func ReadTerminal(text string) (Terminal, bool) {
	for _, line := range strings.Split(text, "\n") {
		rest, found := afterMarker(line)
		if !found {
			continue
		}
		var record Terminal
		if err := json.Unmarshal([]byte(strings.TrimSpace(rest)), &record); err != nil {
			continue
		}

		return record, true
	}

	return Terminal{}, false
}

func afterMarker(line string) (string, bool) {
	for _, marker := range []string{TerminalMarker, HistoricTerminalMarker} {
		if at := strings.Index(line, marker); at >= 0 {
			return line[at+len(marker):], true
		}
	}
	return "", false
}

// Sentence is the record as one sentence: the program's message and, when it
// gave one, the detail that says which element and why.
func (t Terminal) Sentence() string {
	sentence := strings.TrimSuffix(t.Message, ".")
	if t.Detail != "" {
		if sentence != "" {
			sentence += ": "
		}
		sentence += strings.TrimSuffix(t.Detail, ".")
	}
	if sentence == "" {
		return ""
	}

	return sentence + "."
}
