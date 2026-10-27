package build

import (
	"path"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Tool textures, and the line every compiler prints about them.
//
// `skip` has no picture in any WAD: the compiler reads the name and drops the
// face. ericw-tools 2.0 nevertheless prints `WARNING: unable to find texture
// skip` for every Quake 1 map — measured on the Auto-Pigeon build 1.4535 with
// both of a map's WADs opened and found (NEW_357) — and the tool profile's own
// rule says so: "skip and the other tool textures always report this; that is
// expected". Build & Run never counted them. A build counted that line as a
// warning, so every sealed Quake 1 leak test with all of its textures read
// "not a complete result".
//
// So a texture-not-found WARNING that names a tool texture is recorded as
// information: the line stays, word for word, and says why it is not a missing
// texture. Nothing else changes severity — a real texture that is missing is
// still a warning, and an error is still an error.

// MissingTextureRules are the diagnostic rule ids, across the built-in tool
// profiles, that mean "a texture was not found". A profile with other ids is
// simply not read this way.
var MissingTextureRules = map[string]bool{"texture_missing": true, "texture_not_found": true}

// toolTextures have no picture in any WAD by design.
var toolTextures = map[string]bool{
	"skip": true, "clip": true, "hint": true, "hintskip": true, "trigger": true,
	"origin": true, "nodraw": true, "null": true, "areaportal": true, "clipmon": true,
}

// IsToolTexture reports whether a texture name, with or without its folder,
// is one of the compiler's tool textures.
func IsToolTexture(name string) bool {
	return toolTextures[strings.ToLower(path.Base(name))]
}

// TextureNamed is the last word of a compiler's texture line, which is where
// every supported compiler puts it: `WARNING: unable to find texture SKY4`,
// `Couldn't locate texture for e1u1/floor1_3`.
func TextureNamed(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[len(fields)-1], "`'\".,:;()")
}

// expectedToolTextures returns the diagnostics with each texture-not-found
// warning about a tool texture recorded as information.
func expectedToolTextures(diagnostics []job.Diagnostic) []job.Diagnostic {
	out := append([]job.Diagnostic(nil), diagnostics...)
	for i, d := range out {
		if !MissingTextureRules[d.RuleID] || d.Severity != profile.SeverityWarning {
			continue
		}
		name := TextureNamed(d.Raw)
		if name == "" || !IsToolTexture(name) {
			continue
		}
		out[i].Severity = profile.SeverityInfo
		out[i].Message = "`" + name + "` is a tool texture: it has no picture in any WAD, and the compiler always " +
			"reports it. It is not a missing texture."
	}
	return out
}
