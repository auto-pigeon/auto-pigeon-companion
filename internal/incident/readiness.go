package incident

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
)

// CheckReadiness asks the configured AUB whether it is answering, within
// bound, and reports `aucom.readiness_failed` when it does not.
//
// It is the Companion's one readiness check, run once when the local server
// starts (and by `companion dev fault readiness`, which points it at an
// unreachable address — the same function, not a copy). The report carries
// the kind of failure and the bound; never the address, which is where
// somebody's backend lives.
//
// A nil client — no server chosen — is not a readiness failure: the page
// already tells the person to choose one, and there is nothing to measure.
func CheckReadiness(ctx context.Context, client *aub.Client, bound time.Duration, r *Reporter, correlation string) (Incident, error) {
	if client == nil {
		return Incident{}, nil
	}
	if bound <= 0 {
		bound = aub.ReadinessBound
	}
	checkCtx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	started := time.Now()
	err := client.Ready(checkCtx)
	if err == nil {
		return Incident{}, nil
	}
	if ctx.Err() != nil {
		// The Companion itself is stopping; that is not the backend's failure.
		return Incident{}, err
	}
	return r.Capture(ReadinessDraft(err, time.Since(started), bound, correlation)), err
}

// ReadinessDraft is the incident a failed readiness check becomes.
func ReadinessDraft(err error, elapsed, bound time.Duration, correlation string) Draft {
	operation, message := "readiness.unreachable", "The Companion could not reach its Auto-Pigeon server."
	var apiErr *aub.APIError
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		operation = "readiness.timeout"
		message = fmt.Sprintf("The Companion's Auto-Pigeon server did not answer within %d seconds.", int(bound.Round(time.Second)/time.Second))
	case errors.As(err, &apiErr):
		operation = "readiness.refused"
		message = fmt.Sprintf("The Companion's Auto-Pigeon server answered its readiness check with HTTP %d.", apiErr.StatusCode)
	}
	return Draft{
		Severity:    SeverityWarning,
		Subsystem:   "aub.link",
		Code:        CodeReadinessFailed,
		Operation:   operation,
		Message:     message,
		Duration:    elapsed,
		Correlation: correlation,
		UserAction:  "Check the server chosen in Settings, or try again later.",
		Recoverable: true,
	}
}
