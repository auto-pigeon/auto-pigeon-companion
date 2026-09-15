package release

import (
	"regexp"
	"runtime"
	"runtime/debug"
)

// Build is what the binary itself records about how it was built.
//
// It is read from [debug.ReadBuildInfo], which the Go toolchain writes into
// every executable, so an unpacked release can name its source commit on a
// machine with no Go, no Git and no checkout. Nothing here is a claim the
// release script typed: `vcs.revision` and `vcs.modified` are what `go build`
// saw.
type Build struct {
	Commit    string
	Modified  bool
	GoVersion string
	Target    string
	CGO       string
}

// ReadBuild reads this executable's build information.
func ReadBuild() Build {
	build := Build{GoVersion: runtime.Version(), Target: runtime.GOOS + "/" + runtime.GOARCH, CGO: "unknown"}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return build
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			build.Commit = setting.Value
		case "vcs.modified":
			build.Modified = setting.Value == "true"
		case "CGO_ENABLED":
			build.CGO = setting.Value
		}
	}
	return build
}

// versionPattern is the frozen `1.<commit-count>` shape AUP and AUG report.
var versionPattern = regexp.MustCompile(`^1\.[0-9]+$`)

// IsProductVersion reports whether a version string is the frozen shape.
// Anything else — `unknown`, a semantic version, a hash — is not a version a
// person should read out, and surfaces that display one show nothing instead.
func IsProductVersion(version string) bool { return versionPattern.MatchString(version) }
