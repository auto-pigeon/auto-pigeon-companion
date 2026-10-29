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

// fixtureFailServeEnv names a version whose `serve` refuses to start, in a
// FIXTURE BUILD only: the controlled candidate that passes its proving start
// and then never serves, so a live task can watch the old Companion put
// itself back (NEW_368).
const fixtureFailServeEnv = "AUCOM_FIXTURE_FAIL_SERVE"

func fixtureFailServe(version string) bool {
	value := strings.TrimSpace(os.Getenv(fixtureFailServeEnv))
	return value != "" && value == version
}

// fixtureKillAtEnv names a self-update transaction point ("aside:<path>",
// "placed:<path>", "swapped", "restore:<path>", …) at which a FIXTURE BUILD
// kills itself, abruptly, in the middle of replacing itself — so a live task
// can watch the next start, or `selfupdate recover`, put the folder back from
// a real interrupted swap of real bundles (01_NEW_368 §1).
const fixtureKillAtEnv = "AUCOM_FIXTURE_KILL_AT"

func fixtureSelfUpdateFault() func(string) error {
	point := strings.TrimSpace(os.Getenv(fixtureKillAtEnv))
	if point == "" {
		return nil
	}
	return func(at string) error {
		if at == point {
			self, _ := os.FindProcess(os.Getpid())
			_ = self.Kill()
			select {}
		}
		return nil
	}
}
