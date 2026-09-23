package acquire

import (
	"runtime"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// The platform this Companion is running on.
//
// A variable rather than a call to runtime.GOOS everywhere, so a test can ask
// what happens on another machine without needing
// a second machine. Production code never sets them; nothing outside
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

// CurrentPlatform is the platform an executable is resolved for.
func CurrentPlatform() profile.Platform {
	return profile.Platform{OS: goos(), Arch: goarch()}
}
