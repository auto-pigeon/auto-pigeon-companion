package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/texturebundle/fixturewad"
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

// T1 of NEW_633, through the real runner, server and fixture compiler: two
// managed installs of one fake compiler — independently distinguishable bytes,
// each with what its probe said about those bytes — run one after the other.
// Each build records, shows and returns the install that ran, never the
// profile's number and never the other install.
func TestTwoManagedInstallsOfOneCompilerAreToldApartByTheRunsTheyMade(t *testing.T) {
	m, _ := newLeakMachine(t)
	wad := fixturewad.WAD("n633_floor", 8)
	m.saveRoom("new633", 1, q1Room("new633.wad", false, "n633_floor"),
		leakBundle("new633", 1, []string{"new633.wad"}, map[string][]byte{"new633.wad": wad}))
	type ran struct{ version, path, digest, identity, build string }
	var runs []ran
	for i, version := range []string{"9.0001", "9.0002"} {
		program, digest, identity := m.bindManagedFixture(version)
		run := m.startLeak(m.receiveLeakLink(strings.Repeat(strconv.Itoa(i+1), 32), "quake1"), nil)
		if run.status != http.StatusAccepted || run.manifest == nil {
			t.Fatalf("the build on %s: %d %v", version, run.status, run.body)
		}
		id, _ := run.manifest["build_id"].(string)
		runs = append(runs, ran{version, program, digest, identity, id})
	}
	for _, r := range runs {
		manifest, err := build.Find(m.builds, r.build)
		if err != nil {
			t.Fatal(err)
		}
		if len(manifest.Tools) != 1 || manifest.Tools[0].Managed == nil || manifest.Tools[0].Managed.Version != r.version ||
			len(manifest.Tools[0].Executables) != 1 || manifest.Tools[0].Executables[0].SHA256 != r.digest {
			t.Fatalf("the build on %s recorded %+v", r.version, manifest.Tools)
		}
		var started string
		for _, step := range manifest.Steps {
			if step.Command != nil {
				started = step.Command.Executable
			}
		}
		if started != r.path {
			t.Errorf("the build on %s started %q, not its install's %q", r.version, started, r.path)
		}
		_, body := m.call(http.MethodGet, "/api/v1/build/runs/"+r.build, nil)
		steps, _ := body["steps_ran"].(map[string]any)
		row, _ := steps["compile"].(map[string]any)
		if want := "fixture-qbsp " + r.version + " (" + r.identity + ")"; row["label"] != want || row["source"] != build.RanFromExecutable {
			t.Errorf("the build on %s shows its compiler as %v, want %q", r.version, row, want)
		}
		// T4: the label names a program, never where it lives or a token.
		raw, _ := json.Marshal(body["steps_ran"])
		for _, private := range []string{r.path, filepath.Dir(filepath.Dir(r.path)), m.backend.token, m.server.Token().Value()} {
			if private != "" && strings.Contains(string(raw), private) {
				t.Errorf("steps_ran of the build on %s carries a private value %q: %s", r.version, private, raw)
			}
		}
		_, result := m.call(http.MethodGet, "/api/v1/leak-test/runs/"+r.build+"/result", nil)
		if result["compiler_version"] != r.identity {
			t.Errorf("the result of the build on %s names compiler %v, want %q", r.version, result["compiler_version"], r.identity)
		}
	}
}

// bindManagedFixture binds the fixture compiler as the Companion's managed
// install of `version`: a copy of the test binary with bytes of its own (so
// each version has its own digest), the managed record bindManaged writes and
// the fact a compatibility probe leaves about those exact bytes. The probe is
// not run: what is under test is what the runner, the record and the routes
// do with its word.
func (m *machine) bindManagedFixture(version string) (program, digest, identity string) {
	t := m.t
	t.Helper()
	const profileID = "aucom.fixture.leak-qbsp"
	self, err := os.ReadFile(mustExecutable(t))
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := m.server.bindingsPath()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "managed", "fixture-qbsp", version)
	program = filepath.Join(dir, "bin", "tool")
	if err := os.MkdirAll(filepath.Dir(program), 0o700); err != nil {
		t.Fatal(err)
	}
	// Trailing bytes after an executable change its digest and nothing it
	// does: two installs, told apart by what they are.
	bytes := append(append([]byte{}, self...), []byte("\nNEW_633 managed "+version+"\n")...)
	if err := os.WriteFile(program, bytes, 0o700); err != nil {
		t.Fatal(err)
	}
	digest = "sha256:" + sha256Hex(bytes)
	identity = "fixture-qbsp " + version + "+auto-pigeon"
	_, doc := m.call(http.MethodGet, "/api/v1/profiles/"+profileID, nil)
	if status, body := m.call(http.MethodPost, "/api/v1/profiles/"+profileID+"/bind", map[string]any{
		"executables": map[string]string{"tool": program}, "approve": true, "digest": doc["digest"],
	}); status != http.StatusOK {
		t.Fatalf("binding %s: %d %v", version, status, body)
	}
	if _, err := binding.Update(bindings, func(set *binding.Set) error {
		local, _ := set.Find(profileID)
		local.Acquisition = profile.AcquireManagedOfficial
		local.Managed = &binding.ManagedInstall{Tool: "fixture-qbsp", Version: version, Dir: dir}
		local.Compatibility = map[string]binding.ExecutableFact{"tool": {Path: program, SHA256: digest,
			Requirement: "sha256:" + strings.Repeat("0", 64), State: binding.FactCompatible,
			Identity: identity, CheckedAt: time.Now().UTC()}}
		return set.Put(local)
	}); err != nil {
		t.Fatal(err)
	}
	return program, digest, identity
}
