package launch

import (
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

func TestEveryExampleConfigBecomesAValidEngineProfile(t *testing.T) {
	for _, config := range ExampleConfigs() {
		t.Run(config.Game, func(t *testing.T) {
			document, err := EngineProfile(config)
			if err != nil {
				t.Fatalf("generating: %v", err)
			}
			// The same validation a document somebody published goes through.
			// A generated profile that took a shortcut here would be the
			// privileged path this repository keeps saying it does not have.
			if err := document.Validate(); err != nil {
				t.Fatalf("the generated profile is invalid: %v", err)
			}
			if _, err := profile.Digest(document); err != nil {
				t.Fatalf("digesting: %v", err)
			}

			action, found := document.ActionByID(profile.ActionPlayMap)
			if !found {
				t.Fatal("the generated profile has no play_map action")
			}
			if action.SessionRole != profile.SessionClient {
				t.Errorf("session role = %q, want client", action.SessionRole)
			}
			// The map name arrives as a runtime value, which is what makes it
			// something a user types rather than something the document fixes.
			joined := ""
			for _, arg := range action.Args {
				joined += arg.Value + " "
			}
			if !strings.Contains(joined, "{runtime."+profile.RuntimeMapName+"}") {
				t.Errorf("args = %q, want the map as a runtime placeholder", joined)
			}
			if strings.Contains(joined, "{map}") || strings.Contains(joined, "{game_root}") {
				t.Errorf("args = %q, want the launch config's placeholders translated", joined)
			}
		})
	}
}

// TestNothingFromTheMachineEntersTheGeneratedDocument.
//
// The generated profile is a portable document like any other, so the machine's
// own paths belong in the request, not in it. The validator refuses an absolute
// path outright; this asserts the same thing from the other side, against a
// config whose pattern is entirely machine-specific.
func TestNothingFromTheMachineEntersTheGeneratedDocument(t *testing.T) {
	document, err := EngineProfile(Config{
		Game:              "quake",
		ExecutablePattern: "{game_root}/quakespasm{exe}",
		Args:              []string{"-basedir", "{game_root}", "+map", "{map}"},
	})
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	encoded, err := profile.Export(document)
	if err != nil {
		t.Fatalf("exporting: %v", err)
	}
	// Export runs the portability check, which is the one that refuses machine
	// paths, credentials and network locations.
	if len(encoded) == 0 {
		t.Fatal("the export is empty")
	}

	request, err := JobRequest(Config{Game: "quake", ExecutablePattern: "{game_root}/quakespasm{exe}",
		Args: []string{"-basedir", "{game_root}", "+map", "{map}"}}, "/games/quake", "e1m1", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	// The machine's half is in the request instead, where it belongs.
	if request.Executables["engine"] == "" {
		t.Error("the request does not say where the engine is")
	}
	if request.Roots[profile.RootGame] == "" {
		t.Error("the request does not say where the game root is")
	}
	if request.Runtime[profile.RuntimeMapName] != "e1m1" {
		t.Errorf("the map is %q, want e1m1", request.Runtime[profile.RuntimeMapName])
	}
	if request.ProfileID != document.Metadata().ID {
		t.Errorf("the request names %q, the document is %q", request.ProfileID, document.Metadata().ID)
	}
}

func TestAGameWithNoEngineFamilyIsRefusedByName(t *testing.T) {
	_, err := EngineProfile(Config{Game: "doom", ExecutablePattern: "{game_root}/doom{exe}"})
	if err == nil {
		t.Fatal("a game outside AUB's engine families was accepted")
	}
	if !strings.Contains(err.Error(), "quake1") {
		t.Errorf("error = %v, want it to name the families it does know", err)
	}
}

func TestTheGeneratedCatalogSkipsWhatItCannotDescribe(t *testing.T) {
	catalog := NewCatalog(StaticProvider{Items: []Config{
		{Game: "quake", ExecutablePattern: "{game_root}/quakespasm{exe}"},
		{Game: "doom", ExecutablePattern: "{game_root}/doom{exe}"},
	}})
	entries, err := catalog.List()
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	// One unplaceable game must not make every other game unlaunchable.
	if len(entries) != 1 {
		t.Fatalf("the catalog has %d entries, want 1", len(entries))
	}
	if entries[0].Profile.Metadata().ID != GeneratedFor("quake") {
		t.Errorf("the catalog holds %q", entries[0].Profile.Metadata().ID)
	}
	// Asking for the one it skipped still says why, by name.
	if _, err := catalog.Lookup(GeneratedFor("doom")); err == nil {
		t.Error("a skipped game was found in the catalog")
	}
}
