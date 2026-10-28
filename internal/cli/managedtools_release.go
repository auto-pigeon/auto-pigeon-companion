//go:build !aucom_fixture_releases

package cli

import "github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"

// fixtureReleasesEnv names the task fixture seam, which exists only in a
// build made with the aucom_fixture_releases tag. In this build it is never
// read: a released Companion asks GitHub and nothing else.
const fixtureReleasesEnv = "AUCOM_FIXTURE_RELEASES_URL"

func fixtureReleaseSource() (managedtools.Source, bool, error) { return nil, false, nil }

// fixtureFailServe is the fixture build's failure injection; never here.
func fixtureFailServe(string) bool { return false }
