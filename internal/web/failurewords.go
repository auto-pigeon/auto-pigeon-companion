package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/assetsync"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/playrun"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle"
)

// What a refusal or a failure of the Auto-Pigeon server means to a person
// (NEW_363, closing NEW_360 F2 and F3).
//
// NEW_360 found Build & Run printing `aub: /api/companion/v1/catalog: HTTP 401:
// …` under the map list, a Terms refusal that looked exactly like an expired
// sign-in, and a texture download that failed with a 503 telling the person to
// sign in again and choose a newer revision. Each was the server's own words,
// shown raw. The category is decided HERE, once, from the HTTP status and AUB's
// machine-readable reason (`data.reason.code`) — never from the English of a
// message, which is AUB's to change and the page's to translate. The page reads
// the code and says one sentence in the reader's language; the exact text stays
// in the response's `error` for Technical details.
const (
	// codeLegalReview: AUB's legal gate refused the request because the Terms
	// or the notices changed (`legal_acceptance_required`). Only the person can
	// review and accept them, on the Auto-Pigeon site — never this program.
	codeLegalReview = "legal_review_required"
	// codeRefused: the server refused this account for another reason (a 403
	// that is neither the legal gate nor a missing sign-in).
	codeRefused = "refused"
	// codeMapAccessDenied: this account may not read this map.
	codeMapAccessDenied = "map_access_denied"
	// codeServerUnavailable: the server did not answer, or answered that it
	// cannot right now (5xx, 429, a connection that failed or timed out).
	codeServerUnavailable = "server_unavailable"
	// codeRevisionChanged: the map was saved again since its revision was
	// chosen, so the export would not be that revision's.
	codeRevisionChanged = "revision_changed"
	// codeDownloadDamaged: what arrived is not what the server declared. It is
	// never used: a person may download it again, explicitly.
	codeDownloadDamaged = "download_damaged"
)

// reasonLegalAcceptanceRequired is AUB's legal gate's reason code
// (auto-pigeon-backend internal/legalaccept ReasonRequired).
const reasonLegalAcceptanceRequired = "legal_acceptance_required"

// aubFailureCode is the category of an error from (or on the way to) the
// Auto-Pigeon server, or "" when it is none of the above.
func aubFailureCode(err error) string {
	if err == nil {
		return ""
	}
	var refusal *aub.TextureExportRefusal
	if errors.As(err, &refusal) {
		switch {
		case refusal.Reason == reasonLegalAcceptanceRequired:
			return codeLegalReview
		case unavailableStatus(refusal.Status):
			return codeServerUnavailable
		case refusal.StaleRevision():
			return codeRevisionChanged
		case refusal.Reason == aub.ReasonMapAccessDenied:
			return codeMapAccessDenied
		case refusal.Status == http.StatusUnauthorized:
			return codeSignInRequired
		case refusal.Status == http.StatusForbidden:
			return codeRefused
		}
		return ""
	}
	var apiErr *aub.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Reason == reasonLegalAcceptanceRequired:
			return codeLegalReview
		case apiErr.Reason == aub.ReasonMapAccessDenied:
			return codeMapAccessDenied
		case apiErr.StatusCode == http.StatusUnauthorized:
			return codeSignInRequired
		case apiErr.StatusCode == http.StatusForbidden:
			return codeRefused
		case unavailableStatus(apiErr.StatusCode):
			return codeServerUnavailable
		}
		return ""
	}
	if errors.Is(err, assetsync.ErrDigestMismatch) || errors.Is(err, texturebundle.ErrDamaged) {
		return codeDownloadDamaged
	}
	var netErr net.Error
	var urlErr *url.Error
	if errors.As(err, &netErr) || errors.As(err, &urlErr) || errors.Is(err, context.DeadlineExceeded) {
		return codeServerUnavailable
	}
	return ""
}

func unavailableStatus(status int) bool {
	return status >= http.StatusInternalServerError || status == http.StatusTooManyRequests
}

// downloadWords is the summary and the remedy a failed download is shown with,
// by category. English here, for the CLI and Jobs; the page says the same in
// the reader's language from the code (play.js FAILURE_WORDS).
var downloadWords = map[string][2]string{
	codeSignInRequired: {"Your sign-in has expired, so the download could not finish.",
		"Sign in again, then press Try again."},
	codeLegalReview: {"The Terms or the notices changed, so the server will not send anything until you review them.",
		"Review and accept them on the Auto-Pigeon site, then press Try again."},
	codeMapAccessDenied: {"Your account may not read this map.",
		"Ask the map's owner to share it with you."},
	codeRefused: {"The Auto-Pigeon server refused this download.",
		"Technical details show what it answered."},
	codeServerUnavailable: {"The Auto-Pigeon server is temporarily unavailable, so the download could not finish.",
		"Try again in a moment. The same revision is used, and nothing was compiled."},
	codeRevisionChanged: {"The map was saved again after this revision was chosen.",
		"Choose the revision again, then build."},
	codeDownloadDamaged: {"What was downloaded did not match what the server declared, so nothing was compiled.",
		"Press Try again to download it again. If it happens again, report it."},
}

// downloadFailure gives a download's error its category, summary and remedy,
// keeping the error itself — its whole text stays the record's `error`.
func downloadFailure(err error) error {
	code := aubFailureCode(err)
	words, known := downloadWords[code]
	if !known {
		return err
	}
	return &playrun.RemedyError{Err: err, Code: code, Summary: words[0], Remedy: words[1]}
}

// writeAUBError writes an error from the Auto-Pigeon server with its category,
// so the page can say one sentence and offer the action that fixes it.
func writeAUBError(w http.ResponseWriter, err error) {
	writeJSON(w, aubStatus(err), errorBody{Error: err.Error(), Code: aubFailureCode(err)})
}
