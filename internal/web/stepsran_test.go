package web

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// AUCOM #49 (NEW_633): the Build & Run stage row names the program that ran —
// the managed 1.4560 install, with what those exact bytes called themselves —
// and the profile only as the profile. A run recorded before any identity was
// kept says its version was not recorded; it is never given the profile's.
func TestTheBuildViewNamesEachStagesProgramThatRan(t *testing.T) {
	m := newMachine(t)
	const profileID = "auto-pigeon.ericw-tools.auto-pigeon-build.q2"
	const qbsp = "/managed/ericw-tools/1.4560/bin/qbsp"
	save := func(id string, tool build.ToolRecord, executable string) {
		t.Helper()
		started := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
		manifest := &build.Manifest{SchemaVersion: build.SchemaVersion, BuildID: id, State: job.Failed,
			EngineFamily: "quake2", Directory: filepath.Join(m.builds, id),
			Pipeline:  build.DocumentRef{ID: "auto-pigeon.q2.leak-test", Name: "Quake II — leak test"},
			StartedAt: started, FinishedAt: started.Add(time.Second),
			Tools: []build.ToolRecord{tool},
			Steps: []build.Step{{ID: "compile", Capability: "q2.bsp.compile", State: job.Failed,
				Profile: build.DocumentRef{ID: profileID, Version: "1.0.0", Name: tool.Profile.Name},
				Command: &job.CommandPreview{Executable: executable}}},
		}
		if err := os.MkdirAll(manifest.Directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Save(manifest.Directory); err != nil {
			t.Fatal(err)
		}
	}
	doc := build.DocumentRef{ID: profileID, Version: "1.0.0", Name: "EricW tools, Auto-Pigeon build 1.4556 (Quake II, experimental)"}
	writtenFor := "2.0.0-alpha11-70-ge923cca3+auto-pigeon.1.4556"
	save("20261122T080000Z-0000a633", build.ToolRecord{Profile: doc, ToolVersion: writtenFor,
		Acquisition: profile.AcquireManagedOfficial,
		Managed:     &binding.ManagedInstall{Tool: "ericw-tools", Version: "1.4560"},
		Executables: []build.ExecutableRecord{{Name: "qbsp", Path: qbsp, SHA256: "8d4c5f48",
			Identity: "2.0.0-alpha11-74-gc8a1cfaf+auto-pigeon.1.4560"}}}, qbsp)
	save("20261101T080000Z-0000b633", build.ToolRecord{Profile: doc, ToolVersion: writtenFor,
		Acquisition: profile.AcquireUserPath,
		Executables: []build.ExecutableRecord{{Name: "qbsp", Path: "/old/qbsp", SHA256: "11111111"}}}, "/old/qbsp")

	ran := func(id string) map[string]any {
		t.Helper()
		status, body := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
		if status != http.StatusOK {
			t.Fatalf("GET %s: %d %v", id, status, body)
		}
		steps, _ := body["steps_ran"].(map[string]any)
		row, _ := steps["compile"].(map[string]any)
		if row == nil {
			t.Fatalf("GET %s carries no steps_ran for the compile stage: %v", id, body["steps_ran"])
		}
		return row
	}
	current := ran("20261122T080000Z-0000a633")
	if current["source"] != build.RanFromExecutable || current["managed_version"] != "1.4560" ||
		current["label"] != "ericw-tools 1.4560 (2.0.0-alpha11-74-gc8a1cfaf+auto-pigeon.1.4560)" {
		t.Errorf("the stage's program is not the 1.4560 that ran: %v", current)
	}
	if current["profile"] != doc.Name+" 1.0.0, written for "+writtenFor {
		t.Errorf("the profile is not named as the profile: %v", current["profile"])
	}
	old := ran("20261101T080000Z-0000b633")
	if old["source"] != build.RanNotRecorded || old["label"] != "version not recorded for this run" || old["identity"] != nil {
		t.Errorf("an old run without an identity is given one: %v", old)
	}
}
