package managedtools

import (
	"fmt"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile/builtin"
)

// strictProfiles are the built-in profiles whose compatibility rule decides
// whether a release of each tool may be installed: the Auto-Pigeon-build
// profiles, which accept only Auto-Pigeon builds and refuse the releases
// known to be wrong by name.
var strictProfiles = map[ToolID]string{
	EricW:  builtin.EricwAutoPigeonQ1,
	Q3Map2: builtin.Q3Map2AutoPigeon,
}

// BuiltinProbes are those rules, from the documents compiled into this build.
func BuiltinProbes() (map[ToolID]*profile.CompatibilityProbe, error) {
	out := map[ToolID]*profile.CompatibilityProbe{}
	for id, profileID := range strictProfiles {
		entry, err := builtin.Find(profileID)
		if err != nil {
			return nil, err
		}
		tool, ok := entry.Profile.(*profile.ToolProfile)
		if !ok || tool.CompatibilityProbe == nil {
			return nil, fmt.Errorf("managedtools: %s declares no compatibility rule", profileID)
		}
		out[id] = tool.CompatibilityProbe
	}
	return out, nil
}

// StrictProfile is the built-in profile whose rule governs a tool's releases,
// and whose version check verifies an installation before it is bound.
func StrictProfile(id ToolID) string { return strictProfiles[id] }
