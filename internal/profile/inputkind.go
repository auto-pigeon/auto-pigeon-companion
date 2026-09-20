package profile

import "strings"

// The source kinds a declared input can be offered from.
//
// An input is not a generic "file with two sources". A map source, a texture
// collection, an ordinary file and an ordinary directory are four different
// questions to ask a person, and asking the wrong one is what AUCOM/AUT 246I
// found on Windows: every input offered "A map from My Maps" and a single-file
// picker, so the Texture WAD field asked for one WAD — which is not what a Quake
// 1 build reads — and the map field would not show the map that had just been
// downloaded.
//
// This vocabulary is derived from the input's DECLARED role, never from a file
// name, an extension guess or a basename. The role is already the thing a
// pipeline matches producers against ([InputSpec.Role]), so there is no second
// contract here to disagree with the first.
const (
	SourceKindMap       = "map"
	SourceKindTextures  = "textures"
	SourceKindFile      = "file"
	SourceKindDirectory = "directory"
)

// mapSourceSuffix is what every family spells its editable map source with:
// `q1.map.source`, `q2.map.source`, `q3.map.source`.
const mapSourceSuffix = ".map.source"

// familyTextureRoles is the role each engine family declares as its texture
// collection. It is a table and not a suffix rule ON PURPOSE.
//
// Quake 1 reads the WADs a map is connected to. Quake II and Quake III do not:
// they use the texture/material representation their own profiles declare, and
// neither declares one today — no `q2.*` or `q3.*` pipeline in
// internal/profile/builtin has a texture input at all. A suffix rule like
// "anything ending .wad is textures" would therefore invent a texture input for
// a family that never asked for one and hand it Quake 1 semantics, which is
// exactly the thing 246I forbids: never silently treat a Q1 WAD as a universal
// texture format. A family absent from this table has no texture source, and an
// input it declares is an ordinary file.
//
// Adding a family here is a deliberate act that says "this is how that engine
// represents textures", and TestOnlyQuake1HasATextureSourceToday is what fails
// if one arrives without one.
var familyTextureRoles = map[string]string{
	"quake1": "q1.wad",
}

// InputSourceKind says which question to ask for an input of this role in this
// engine family.
//
// Unknown roles are ordinary files rather than an error: a pipeline may declare
// an input this build of the Companion has never heard of, and "type a path to
// it" is the honest fallback. There is deliberately no case that returns
// [SourceKindDirectory] from a builtin today — no builtin declares a plain
// directory input — but the kind exists so a profile that declares one is
// rendered as a folder chooser instead of being silently offered a file picker.
func InputSourceKind(role, family string) string {
	if role == "" {
		return SourceKindFile
	}
	if strings.HasSuffix(role, mapSourceSuffix) {
		return SourceKindMap
	}
	if textures, ok := familyTextureRoles[family]; ok && role == textures {
		return SourceKindTextures
	}
	if strings.HasSuffix(role, ".directory") {
		return SourceKindDirectory
	}
	return SourceKindFile
}
