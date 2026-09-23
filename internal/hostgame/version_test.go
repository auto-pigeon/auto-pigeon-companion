package hostgame_test

import (
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/hostgame"
)

// The version a listing publishes is the one the running engine printed, not
// the profile's supported range.
func TestTheListedVersionIsTheOneTheRunningEnginePrinted(t *testing.T) {
	// The first lines of the vkQuake 1.36.0 job the finding came from.
	vkquake := "Command line: /tmp/x/AppRun -basedir /q -game auto-pigeon +map dm2\n" +
		"Using SDL version 3.4.12\nDetected 8 CPUs.\nInitializing vkQuake 1.36.0\nBuilt with GCC 11.4.0\n" +
		"vkQuake 1.36.0 Server (59507 CRC)\n"
	for _, c := range []struct {
		runtime, output, want string
		ok                    bool
	}{
		{"vkquake", vkquake, "1.36.0", true},
		{"quakespasm", "QuakeSpasm 0.96.3 (Linux 64-bit)\n", "0.96.3", true},
		{"ironwail", "Ironwail v0.7.0\n", "0.7.0", true},
		{"darkplaces", "DarkPlaces 20180908-beta1\n", "", false},
		{"fteqw", "FTE QuakeWorld 1.05-beta1\n", "", false},
		// Another program's version on the same line is not the engine's.
		{"vkquake", "Using SDL version 3.4.12\n", "", false},
		{"vkquake", "vkQuake Server (59507 CRC)\n", "", false},
		{"vkquake", "", "", false},
		{"", vkquake, "", false},
	} {
		got, ok := hostgame.ObservedEngineVersion(c.runtime, c.output)
		if got != c.want || ok != c.ok {
			t.Errorf("ObservedEngineVersion(%q, %q) = %q, %v; want %q, %v", c.runtime, c.output, got, ok, c.want, c.ok)
		}
	}
}
