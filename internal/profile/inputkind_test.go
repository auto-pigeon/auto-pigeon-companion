package profile

import "testing"

func TestInputSourceKindComesFromTheDeclaredRole(t *testing.T) {
	for _, c := range []struct {
		role, family, want string
	}{
		{"q1.map.source", "quake1", SourceKindMap},
		{"q2.map.source", "quake2", SourceKindMap},
		{"q3.map.source", "quake3", SourceKindMap},
		{"q1.wad", "quake1", SourceKindTextures},
		{"q1.bsp", "quake1", SourceKindFile},
		{"something.directory", "quake1", SourceKindDirectory},
		{"", "quake1", SourceKindFile},
		{"a.role.nobody.declared", "quake1", SourceKindFile},
	} {
		if got := InputSourceKind(c.role, c.family); got != c.want {
			t.Errorf("InputSourceKind(%q, %q) = %q, want %q", c.role, c.family, got, c.want)
		}
	}
}

// TestAQuake1WadIsNotAUniversalTextureFormat is the refusal 246I asked for.
//
// The same role spelling must not acquire Quake 1's texture meaning just because
// it appears under another family, and a family that declares no texture
// representation has no texture source at all. Both would present "Textures in
// the Cloud" for an engine that does not read WADs.
func TestAQuake1WadIsNotAUniversalTextureFormat(t *testing.T) {
	for _, family := range []string{"quake2", "quake3", "", "quake4"} {
		if got := InputSourceKind("q1.wad", family); got == SourceKindTextures {
			t.Errorf("q1.wad in family %q was classified as textures", family)
		}
		for _, role := range []string{"q2.wad", "q3.wad", "wad"} {
			if got := InputSourceKind(role, family); got == SourceKindTextures {
				t.Errorf("role %q in family %q was classified as textures", role, family)
			}
		}
	}
}

// TestOnlyQuake1HasATextureSourceToday pins the table itself.
//
// If a family is added to familyTextureRoles, that is a statement about how that
// engine represents textures and it needs its own evidence — so this fails until
// somebody updates it deliberately, rather than letting a family inherit Quake
// 1's answer by accident.
func TestOnlyQuake1HasATextureSourceToday(t *testing.T) {
	if len(familyTextureRoles) != 1 {
		t.Fatalf("familyTextureRoles has %d entries: %v — see the comment there before changing this",
			len(familyTextureRoles), familyTextureRoles)
	}
	if familyTextureRoles["quake1"] != "q1.wad" {
		t.Errorf("quake1 texture role = %q, want q1.wad", familyTextureRoles["quake1"])
	}
}
