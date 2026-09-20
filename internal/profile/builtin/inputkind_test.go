package builtin

import (
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// TestEveryBuiltinPipelineInputClassifies runs the source-kind rule over the
// REAL embedded pipeline documents rather than over a table copied out of them.
//
// AUCOM/AUT 246I: the Build page used to derive an input's sources from one
// generic assumption, so a Texture WAD field offered "A map from My Maps" and a
// single-file picker. The rule that replaced it is only as good as the documents
// it is asked about, which is why this reads them.
func TestEveryBuiltinPipelineInputClassifies(t *testing.T) {
	entries, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	pipelines := 0
	maps := 0
	textures := 0
	for _, entry := range entries {
		pipeline, isPipeline := entry.Profile.(*profile.PipelineProfile)
		if !isPipeline {
			continue
		}
		pipelines++
		family := ""
		if pipeline.GameProfile != nil {
			family = pipeline.GameProfile.EngineFamily
		}
		if family == "" {
			t.Errorf("%s declares no engine family, so its inputs cannot be classified", entry.File)
			continue
		}
		for _, input := range pipeline.Inputs {
			kind := profile.InputSourceKind(input.Role, family)
			switch kind {
			case profile.SourceKindMap:
				maps++
			case profile.SourceKindTextures:
				textures++
				// Only Quake 1 reads the WADs a map is connected to. A texture
				// source appearing under another family is this rule leaking.
				if family != "quake1" {
					t.Errorf("%s (%s): input %q role %q was classified as textures, "+
						"but only quake1 declares a texture representation",
						entry.File, family, input.Name, input.Role)
				}
			case profile.SourceKindFile, profile.SourceKindDirectory:
				// An ordinary path. Nothing to assert beyond it not being one of
				// the two special questions.
			default:
				t.Errorf("%s: input %q classified as %q, which is not one of the four kinds",
					entry.File, input.Name, kind)
			}
		}
	}

	if pipelines == 0 {
		t.Fatal("no builtin pipelines were loaded, so this proved nothing")
	}
	// Every family's editable source must be recognised as a map, or the Build
	// page cannot offer a downloaded revision for it — defect 3.
	if maps != pipelines {
		t.Errorf("%d pipelines but %d map-source inputs: every pipeline declares exactly one", pipelines, maps)
	}
	// And the Quake 1 WAD input must still be found, or this test would pass by
	// classifying everything as an ordinary file.
	if textures == 0 {
		t.Error("no texture input was found in any builtin pipeline; the quake1 wad input should classify as textures")
	}
}
