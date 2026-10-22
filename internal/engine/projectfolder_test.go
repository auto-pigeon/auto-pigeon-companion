package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

func engineOf(family string) *profile.EngineProfile {
	return &profile.EngineProfile{
		GameProfile: profile.GameProfileRef{EngineFamily: family},
		Actions: []profile.Action{{ID: profile.ActionPlayMap, Roots: []profile.RootRef{
			{Role: profile.RootGame, Access: profile.AccessRead},
			{Role: profile.RootContent, Access: profile.AccessRead},
		}}},
	}
}

// NEW_330, HITL 2026-10-22: a Quake 1 or Quake II engine's project folder is
// the Companion's own, created for it; one a person named is kept; Quake III's
// is never guessed.
func TestAnEngineGetsTheCompanionsProjectFolderUnlessOneWasNamed(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "downloads with spaces", "assets")

	var fresh binding.LocalBinding
	filled, err := FillProjectFolder(engineOf("quake1"), &fresh, folder)
	if err != nil || !filled || fresh.Roots[profile.RootContent] != folder {
		t.Fatalf("a Quake 1 engine with no project folder: filled %v, %v, roots %v", filled, err, fresh.Roots)
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		t.Fatalf("the project folder was not created: %v", err)
	}

	named := binding.LocalBinding{Roots: map[string]string{profile.RootContent: "/somewhere/mine"}}
	if filled, _ := FillProjectFolder(engineOf("quake2"), &named, folder); filled || named.Roots[profile.RootContent] != "/somewhere/mine" {
		t.Errorf("a folder the person named was replaced: %v", named.Roots)
	}

	var q3 binding.LocalBinding
	if filled, _ := FillProjectFolder(engineOf("quake3"), &q3, folder); filled || q3.Roots[profile.RootContent] != "" {
		t.Errorf("a Quake III engine's package folder was guessed: %v", q3.Roots)
	}
}
