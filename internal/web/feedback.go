package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/build"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/feedback"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/maturity"
)

// The compatibility report, as the local API composes it.
//
// # Why the browser posts fields and gets a document back
//
// Exactly the reasoning `profiles.go` gives for the profile wizard, applied to
// the thing it matters most for. A page that assembled the JSON itself would be
// a second implementation of what may be shared — and the first time the two
// disagreed, the disagreement would be a user sending something they were told
// they were not sending.
//
// So the page posts what the user typed and what they ticked, this side runs
// [feedback.Build] — the same function `companion feedback compatibility` runs
// — and what comes back is the real document, validated. What the page then
// shows is not a preview of a report it hopes to produce; it is the report.
//
// # And why there is no route that sends it
//
// There is nowhere to POST a finished report to, in this build or in this
// route table. The page shows the document and offers to save it. Adding a
// destination is a decision about somebody else's data and belongs in a task
// that is about that, not in a helper that already has the material in hand.

func (s *Server) feedbackAPI() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"POST /api/v1/feedback/compatibility": s.handleFeedbackCompatibility,
	}
}

// feedbackRequest is what the dialog sends.
//
// `Consent` is the four booleans, absent meaning false, which is what makes
// "the page forgot to send them" identical to "the user ticked nothing".
type feedbackRequest struct {
	EngineFamily string            `json:"engine_family"`
	Summary      string            `json:"summary"`
	Description  string            `json:"description,omitempty"`
	Operation    string            `json:"operation,omitempty"`
	Editor       string            `json:"editor_version,omitempty"`
	Consent      feedback.Consent  `json:"share"`
	Profiles     []profileRefInput `json:"profiles,omitempty"`
	// BuildID fills the operation, the profiles and the diagnostics from a
	// build this machine ran. The manifest also holds every path and every job
	// id, and none of that is read here — see the CLI's `fillFromBuild` for the
	// same rule stated at the other call site.
	BuildID string `json:"build_id,omitempty"`
}

type profileRefInput struct {
	Role        string `json:"role"`
	ID          string `json:"id"`
	Version     string `json:"version,omitempty"`
	ToolVersion string `json:"tool_version,omitempty"`
}

func (s *Server) handleFeedbackCompatibility(w http.ResponseWriter, r *http.Request) {
	var request feedbackRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// A report is offered for a work-in-progress family and for no other. The
	// refusal is deliberate rather than permissive: a report route that
	// accepted any family would be a general-purpose way to have this program
	// assemble a document about the user's machine.
	if !maturity.Of(request.EngineFamily).FeedbackInvited {
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"this build has no compatibility report to file for %q; work-in-progress families are: %s",
			request.EngineFamily, strings.Join(maturity.Families(), ", ")))
		return
	}

	input := feedback.Input{
		EngineFamily:     request.EngineFamily,
		Summary:          request.Summary,
		Description:      request.Description,
		Operation:        request.Operation,
		CompanionVersion: s.version,
		EditorVersion:    request.Editor,
		Platform:         currentPlatform().String(),
	}
	for _, ref := range request.Profiles {
		input.Profiles = append(input.Profiles, feedback.ProfileRef{
			Role: ref.Role, ID: ref.ID, Version: ref.Version, ToolVersion: ref.ToolVersion,
		})
	}

	withheld := 0
	if request.BuildID != "" {
		dir, err := s.buildsDir()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		manifest, err := build.Find(dir, request.BuildID)
		if err != nil {
			writeError(w, jobStatus(err), err)
			return
		}
		if input.EngineFamily == "" {
			input.EngineFamily = manifest.EngineFamily
		}
		if input.Operation == "" {
			input.Operation = "build"
		}
		input.Profiles = append(input.Profiles, feedback.ProfileRef{
			Role: "pipeline", ID: manifest.Pipeline.ID, Version: manifest.Pipeline.Version,
		})
		for _, tool := range manifest.Tools {
			input.Profiles = append(input.Profiles, feedback.ProfileRef{
				Role: "tool", ID: tool.Profile.ID, Version: tool.Profile.Version, ToolVersion: tool.ToolVersion,
			})
		}
		var found []feedback.Occurrence
		for _, step := range manifest.Steps {
			for _, d := range step.Diagnostics {
				found = append(found, feedback.Occurrence{
					RuleID: d.RuleID, Severity: string(d.Severity), Message: d.Message,
				})
			}
		}
		input.Diagnostics, withheld = feedback.Summarize(found)
	}

	report, err := feedback.Build(input, request.Consent, time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	document, err := feedback.Encode(report)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"report":   report,
		"document": string(document),
		// What was left out, and why, so the page can say it rather than the
		// user discovering a gap in a file they already sent.
		"messages_withheld": withheld,
		"sent":              false,
		"filename":          "auto-pigeon-compatibility-" + report.CreatedOn + ".json",
	})
}
