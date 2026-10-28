//go:build aucom_fixture_releases

package cli

import (
	"os"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
)

// fixtureReleasesEnv points a FIXTURE BUILD at a loopback stand-in for
// GitHub's releases: a task's controlled update tests (NEW_367 §6), which
// must not publish fake releases. Only a build made with
// `-tags aucom_fixture_releases` contains this file, and the source it makes
// refuses every address that is not loopback.
const fixtureReleasesEnv = "AUCOM_FIXTURE_RELEASES_URL"

func fixtureReleaseSource() (managedtools.Source, bool, error) {
	base := strings.TrimSpace(os.Getenv(fixtureReleasesEnv))
	if base == "" {
		return nil, false, nil
	}
	source, err := managedtools.NewLoopbackSource(base, nil)
	return source, err == nil, err
}
