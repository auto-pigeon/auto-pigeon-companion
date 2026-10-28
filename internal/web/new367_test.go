package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	mt "github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools/managedtoolstest"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// NEW_367: the managed toolchains, through this server — verified by the
// executor's own version check, bound by the one binding writer, consumed by
// the readiness every page reads, pinned by a real build.

type toolsClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *toolsClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *toolsClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type toolsMachine struct {
	*machine
	releases *mt.Server
	tools    *managedtools.Manager
	clock    *toolsClock
}

func newToolsMachine(t *testing.T) *toolsMachine {
	t.Helper()
	m := newMachine(t)
	releases := mt.NewServer(t)
	releases.Publish("auto-pigeon/ericw-tools", mt.Build(t, mt.Spec{Tool: managedtools.EricW, Version: "1.4536", Prerelease: true}))
	releases.Publish("auto-pigeon/q3map2", mt.Build(t, mt.Spec{Tool: managedtools.Q3Map2, Version: "2.5.17n-ap.3031", Prerelease: true}))
	probes, err := managedtools.BuiltinProbes()
	if err != nil {
		t.Fatal(err)
	}
	clock := &toolsClock{now: time.Date(2026, 10, 28, 20, 0, 0, 0, time.UTC)}
	tools, err := managedtools.New(managedtools.Options{
		ToolsRoot: filepath.Join(m.dir, "tools"),
		StatePath: filepath.Join(m.dir, "config", managedtools.StateFileName),
		Source:    releases.Source(t),
		Host:      m.server.ManagedToolsHost(),
		Probes:    probes,
		Now:       clock.Now,
		Rand:      func() float64 { return 0 },
		FreeSpace: func(string) int64 { return 1 << 40 },
	})
	if err != nil {
		t.Fatal(err)
	}
	m.server.SetManagedTools(tools)
	return &toolsMachine{machine: m, releases: releases, tools: tools, clock: clock}
}

func (m *toolsMachine) step() { m.tools.Step(context.Background()) }

func (m *toolsMachine) binding(id string) (binding.LocalBinding, bool) {
	m.t.Helper()
	set, err := binding.LoadFile(m.bindings)
	if err != nil {
		m.t.Fatal(err)
	}
	return set.Find(id)
}

func (m *toolsMachine) active(id managedtools.ToolID) managedtools.InstallRecord {
	m.t.Helper()
	record, ok := m.tools.ActiveInstall(id)
	if !ok {
		m.t.Fatalf("%s has no active installation: %+v", id, m.tools.Status())
	}
	return record
}

var ericwProfiles = []string{
	"auto-pigeon.ericw-tools.q1", "auto-pigeon.ericw-tools.q2",
	"auto-pigeon.ericw-tools.auto-pigeon-build.q1", "auto-pigeon.ericw-tools.auto-pigeon-build.q2",
}

// M1/§1: one EricW installation serves Quake 1 and Quake II; every built-in
// profile nobody chose is bound to it, verified by the programs themselves,
// and the readiness every page reads says so.
func TestTheManagedEricWIsVerifiedAndBindsQuake1AndQuakeIIToOneInstallation(t *testing.T) {
	m := newToolsMachine(t)
	m.step()
	ericw := m.active(managedtools.EricW)
	for _, id := range ericwProfiles {
		local, found := m.binding(id)
		if !found || local.Acquisition != profile.AcquireManagedOfficial || local.Managed == nil {
			t.Fatalf("%s is not bound to the managed installation: %+v", id, local)
		}
		if local.Managed.Dir != ericw.Dir || local.Managed.SHA256 != ericw.ActualSHA256 {
			t.Fatalf("%s names %+v; the installation is %s", id, local.Managed, ericw.Dir)
		}
		for name, path := range local.Executables {
			if !strings.HasPrefix(path, ericw.Dir+string(filepath.Separator)) {
				t.Errorf("%s's %s is %s, outside the one installation", id, name, path)
			}
		}
		if fact := local.Compatibility["qbsp"]; fact.State != binding.FactCompatible || fact.Identity != ericw.Banner {
			t.Errorf("%s's qbsp was not asked, or said something else: %+v", id, fact)
		}
		status, body := m.call(http.MethodGet, "/api/v1/profiles/"+id, nil)
		if status != http.StatusOK {
			t.Fatalf("%s: %d", id, status)
		}
		if state, _ := body["setup_state"].(string); state == "needs_setup" || state == "incompatible" || state == "unavailable" {
			t.Errorf("%s reads %q after the managed setup: %v", id, state, body["problems"])
		}
	}
	q3 := m.active(managedtools.Q3Map2)
	if local, _ := m.binding("auto-pigeon.q3map2"); local.Managed == nil || local.Managed.Dir != q3.Dir {
		t.Fatalf("q3map2 is not bound: %+v", local)
	}
	if got := m.releases.Downloads[mt.ArchiveName(mustTool(managedtools.EricW), "1.4536", mt.ThisTarget())]; got != 1 {
		t.Fatalf("the EricW archive was downloaded %d times for two games", got)
	}

	status, body := m.call(http.MethodGet, "/api/v1/tools", nil)
	if status != http.StatusOK || body["available"] != true || body["setting_up"] == true {
		t.Fatalf("GET /api/v1/tools = %d: %v", status, body)
	}
}

func mustTool(id managedtools.ToolID) managedtools.Tool {
	tool, _ := managedtools.Find(id)
	return tool
}

// §1: a program somebody chose is never replaced; the managed installation
// is offered beside it, and taken only when they ask.
func TestAProgramSomebodyChoseIsLeftAloneUntilTheyChooseTheManagedOne(t *testing.T) {
	m := newToolsMachine(t)
	// The person's own qbsp: a program of theirs, not the test binary, whose
	// version check would run this whole test suite.
	self := filepath.Join(m.dir, "my-own-ericw", "qbsp")
	if err := os.MkdirAll(filepath.Dir(self), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(self, mt.FixtureProgram(t), 0o755); err != nil {
		t.Fatal(err)
	}
	set, _ := binding.LoadFile(m.bindings)
	catalog, _ := m.server.catalog()
	entry, err := catalog.Lookup("auto-pigeon.ericw-tools.q1")
	if err != nil {
		t.Fatal(err)
	}
	_ = set.Put(binding.LocalBinding{ProfileID: entry.Profile.Metadata().ID, ProfileVersion: entry.Profile.Metadata().Version,
		ProfileDigest: entry.Digest, Trust: entry.Trust, Acquisition: profile.AcquireUserPath,
		Executables: map[string]string{"qbsp": self}, UpdatedAt: time.Now().UTC()})
	if err := binding.SaveFile(m.bindings, set); err != nil {
		t.Fatal(err)
	}
	m.step()
	local, _ := m.binding("auto-pigeon.ericw-tools.q1")
	if local.Acquisition != profile.AcquireUserPath || local.Executables["qbsp"] != self || local.Managed != nil {
		t.Fatalf("the person's own qbsp was replaced: %+v", local)
	}
	if q2, _ := m.binding("auto-pigeon.ericw-tools.q2"); q2.Managed == nil {
		t.Fatal("the profile nobody chose for was not bound")
	}
	_, body := m.call(http.MethodGet, "/api/v1/tools", nil)
	rows, _ := body["profiles"].(map[string]any)["ericw-tools"].([]any)
	custom := false
	for _, row := range rows {
		r := row.(map[string]any)
		if r["id"] == "auto-pigeon.ericw-tools.q1" {
			custom = r["custom"] == true
		}
	}
	if !custom {
		t.Fatalf("the person's own binding is not shown as theirs: %v", rows)
	}
	status, answer := m.call(http.MethodPost, "/api/v1/profiles/auto-pigeon.ericw-tools.q1/use-managed", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("use-managed = %d: %v", status, answer["error"])
	}
	if local, _ := m.binding("auto-pigeon.ericw-tools.q1"); local.Managed == nil || local.Acquisition != profile.AcquireManagedOfficial {
		t.Fatalf("after choosing the managed installation: %+v", local)
	}
	// And choosing a program again takes it back out of the Companion's hands.
	status, answer = m.call(http.MethodPost, "/api/v1/profiles/auto-pigeon.ericw-tools.q1/bind",
		map[string]any{"executables": map[string]string{"qbsp": self}})
	if status != http.StatusOK {
		t.Fatalf("bind = %d: %v", status, answer["error"])
	}
	if local, _ := m.binding("auto-pigeon.ericw-tools.q1"); local.Managed != nil || local.Acquisition != profile.AcquireUserPath {
		t.Fatalf("a program chosen after the managed one: %+v", local)
	}
}

// M6/M8: Update now waits for a running pipeline; the switch moves every
// EricW binding at once; a real build afterwards records exactly the bytes of
// the installation it ran, and nothing a later check finds changes that.
func TestAnUpdateWaitsForARunningBuildAndTheNextBuildRecordsTheInstalledBytes(t *testing.T) {
	m := newToolsMachine(t)
	m.step()
	old := m.active(managedtools.EricW)
	m.releases.Publish("auto-pigeon/ericw-tools", mt.Build(t, mt.Spec{Tool: managedtools.EricW, Version: "1.4540", Prerelease: true}))
	m.clock.advance(2 * time.Hour)
	m.step()
	_, body := m.call(http.MethodGet, "/api/v1/tools", nil)
	offer := offerIn(t, body, "ericw-tools")

	// A pipeline is running: it holds the gate, as build.Runner.Run does.
	release := m.server.toolGate.hold()
	status, answer := m.call(http.MethodPost, "/api/v1/tools/ericw-tools/decide", map[string]any{
		"decision": "now", "version": offer["version"], "asset_id": offer["asset_id"], "sha256": offer["sha256"]})
	if status != http.StatusOK {
		t.Fatalf("decide = %d: %v", status, answer["error"])
	}
	m.step()
	if got := m.active(managedtools.EricW); got.Version != "1.4536" {
		t.Fatalf("activated %s while a build ran", got.Version)
	}
	for _, id := range ericwProfiles {
		if local, _ := m.binding(id); local.Managed.Version != "1.4536" {
			t.Fatalf("%s moved while a build ran", id)
		}
	}
	tool := toolIn(t, m.server.toolsStatus().Status, managedtools.EricW)
	if tool.Pending == nil || !tool.Pending.Staged || len(tool.Pending.Waiting) == 0 {
		t.Fatalf("pending while a build runs: %+v", tool.Pending)
	}
	release()
	m.step()
	updated := m.active(managedtools.EricW)
	if updated.Version != "1.4540" {
		t.Fatalf("after the build: %s", updated.Version)
	}
	for _, id := range ericwProfiles {
		if local, _ := m.binding(id); local.Managed.Version != "1.4540" || local.Managed.Dir != updated.Dir {
			t.Fatalf("%s did not move with the others: %+v", id, local.Managed)
		}
	}
	if _, err := os.Stat(old.Programs["qbsp"]); err != nil {
		t.Fatalf("the previous installation was removed: %v", err)
	}

	// A real build on the managed programs.
	source := filepath.Join(m.content, "room.map")
	writeFixtureFile(t, source, "{ \"classname\" \"worldspawn\" }\n")
	status, started := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline": "auto-pigeon.q1.fast-preview", "inputs": map[string]string{"source_map": source}, "label": "managed tools"})
	if status != http.StatusAccepted {
		t.Fatalf("starting the build = %d: %v", status, started["error"])
	}
	manifest := m.waitForBuild(started["build"].(string))
	if manifest["state"] != "succeeded" {
		t.Fatalf("the build on managed tools: %v %v", manifest["state"], manifest["error"])
	}
	record := managedToolRecord(t, manifest)
	if record["version"] != "1.4540" || record["sha256"] != updated.ActualSHA256 || record["dir"] != updated.Dir {
		t.Fatalf("the build's provenance: %v; installed %s %s", record, updated.Version, updated.ActualSHA256)
	}
	qbsp, _ := os.ReadFile(updated.Programs["qbsp"])
	sum := sha256.Sum256(qbsp)
	if !strings.Contains(string(mustJSON(t, manifest)), hex.EncodeToString(sum[:])) {
		t.Fatal("the build does not record the digest of the qbsp it ran")
	}

	// A later check that finds another release changes nothing recorded.
	before := string(mustJSON(t, manifest))
	m.releases.Publish("auto-pigeon/ericw-tools", mt.Build(t, mt.Spec{Tool: managedtools.EricW, Version: "1.4541", Prerelease: true}))
	m.clock.advance(2 * time.Hour)
	m.step()
	if after := string(mustJSON(t, m.waitForBuild(started["build"].(string)))); after != before {
		t.Fatal("a release check changed a finished build's record")
	}
}

// The gate closes the race: an activation refuses while a pipeline holds it.
func TestAnActivationNeverLandsWhileAPipelineHoldsTheTools(t *testing.T) {
	m := newToolsMachine(t)
	release := m.server.toolGate.hold()
	defer release()
	record := managedtools.InstallRecord{Version: "x", Programs: map[string]string{}}
	if _, err := m.server.bindManaged(mustTool(managedtools.EricW), record, nil); err != errToolsInUse {
		t.Fatalf("binding while a pipeline runs: %v", err)
	}
}

func offerIn(t *testing.T, body map[string]any, id string) map[string]any {
	t.Helper()
	for _, item := range body["tools"].([]any) {
		tool := item.(map[string]any)
		if tool["id"] == id {
			if offer, ok := tool["offer"].(map[string]any); ok {
				return offer
			}
		}
	}
	t.Fatalf("no offer for %s: %v", id, body["tools"])
	return nil
}

func toolIn(t *testing.T, status managedtools.Status, id managedtools.ToolID) managedtools.ToolStatus {
	for _, tool := range status.Tools {
		if tool.ID == id {
			return tool
		}
	}
	t.Fatalf("no %s", id)
	return managedtools.ToolStatus{}
}

func managedToolRecord(t *testing.T, manifest map[string]any) map[string]any {
	t.Helper()
	tools, _ := manifest["tools"].([]any)
	for _, item := range tools {
		if managed, ok := item.(map[string]any)["managed"].(map[string]any); ok {
			return managed
		}
	}
	t.Fatalf("the build records no managed installation: %v", tools)
	return nil
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
