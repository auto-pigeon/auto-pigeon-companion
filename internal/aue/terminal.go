package aue

import (
	"encoding/json"
	"strings"
)

// TerminalMarker is how AUE's published protocol begins its last stderr line.
const TerminalMarker = "AUE-TERMINAL/1.0 "

// Terminal is the part of AUE's terminal record a person is shown.
type Terminal struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Detail  string `json:"detail"`
}

// ReadTerminal finds AUE's terminal record inside an error's text.
//
// It lives here because two surfaces read it — Build & Run's Activity card and,
// since `Q3_010`, every build that converts a map — and the extractor's own
// refusal (`apmap-to-q3map refused [q3map_shader_unsafe]: …`) is inside this
// record's `detail`, after an exit code and before an incident envelope. A
// surface that printed the stderr whole showed the reason and made it
// unfindable in the same line.
func ReadTerminal(text string) (Terminal, bool) {
	for _, line := range strings.Split(text, "\n") {
		at := strings.Index(line, TerminalMarker)
		if at < 0 {
			continue
		}
		var record Terminal
		if err := json.Unmarshal([]byte(strings.TrimSpace(line[at+len(TerminalMarker):])), &record); err != nil {
			continue
		}

		return record, true
	}

	return Terminal{}, false
}

// Sentence is the record as one sentence: the extractor's message and, when it
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
