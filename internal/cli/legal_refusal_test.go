package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
)

// AUB NEW_476: a Terms refusal is not a failed session. The CLI says what to do
// and where, and never tells the person to sign in again.
func TestTheCLIReportsALegalRefusalWithItsAcceptanceAddress(t *testing.T) {
	env, _, stderr := testEnv(t)
	err := fmt.Errorf("listing: %w", &aub.APIError{
		StatusCode: 403, Path: "/api/companion/v1/catalog", Reason: aub.ReasonLegalAcceptanceRequired,
		AcceptanceURL: "https://beta.example.test/legal/accept",
	})
	if code := reportAUBError(env, err); code != 1 {
		t.Fatalf("exit %d", code)
	}
	out := stderr.String()
	if !strings.Contains(out, "https://beta.example.test/legal/accept") {
		t.Errorf("the acceptance address is missing: %s", out)
	}
	if strings.Contains(out, "auth login") || strings.Contains(out, "rejected this session") {
		t.Errorf("a Terms refusal was reported as a failed sign-in: %s", out)
	}

	env, _, stderr = testEnv(t)
	reportAUBError(env, &aub.APIError{StatusCode: 403, Reason: aub.ReasonLegalAcceptanceRequired})
	if !strings.Contains(stderr.String(), "did not say its address") {
		t.Errorf("without an address the CLI must say so: %s", stderr.String())
	}
}
