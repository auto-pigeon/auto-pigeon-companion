package acquire

import (
	"runtime"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// The platform this Companion is running on.
//
// A variable rather than a call to runtime.GOOS everywhere, so a test can ask
// what happens when a catalogue has no build for the running machine without
// needing a second machine. Production code never sets them; nothing outside
// this package can.
var (
	goosOverride   string
	goarchOverride string
)

func goos() string {
	if goosOverride != "" {
		return goosOverride
	}
	return runtime.GOOS
}

func goarch() string {
	if goarchOverride != "" {
		return goarchOverride
	}
	return runtime.GOARCH
}

// CurrentPlatform is the platform a managed download resolves for.
//
// There is deliberately no way to ask for another one. The Companion downloads
// the tool it is going to run, on the machine it is running on; a
// cross-platform acquisition would produce a cache entry that this machine can
// verify and cannot execute, which is a worse answer than the error.
func CurrentPlatform() profile.Platform {
	return profile.Platform{OS: goos(), Arch: goarch()}
}
