package web

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
	mt "github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools/managedtoolstest"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/tooluse"
)

// 01_NEW_368 §2, through the real server: the protection is the tools
// folder's, so another process's build holds a compiler update exactly as
// this one's does, and a pipeline keeps the programs it started with.

const leaseHelperFlag = "-aucom-lease-helper"

// leaseHelperMain is "another Companion process with a build running": it
// takes a lease on the tools folder and holds it until its stdin closes.
func leaseHelperMain(args []string) int {
	if len(args) < 2 {
		return 2
	}
	registry, err := tooluse.Open(args[0])
	if err != nil {
		return 3
	}
	lease, err := registry.Use(strings.Split(args[1], ","), "companion build run (test process)", time.Second)
	if err != nil {
		os.Stdout.WriteString("refused: " + err.Error() + "\n")
		return 4
	}
	os.Stdout.WriteString("held\n")
	_, _ = io.Copy(io.Discard, os.Stdin)
	lease.Release()
	return 0
}

// otherProcess starts the helper and waits until it holds its lease.
func otherProcess(t *testing.T, root string, tools ...string) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	command := exec.Command(os.Args[0], leaseHelperFlag, root, strings.Join(tools, ","))
	stdin, _ := command.StdinPipe()
	stdout, _ := command.StdoutPipe()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	line, _ := bufio.NewReader(stdout).ReadString('\n')
	if strings.TrimSpace(line) != "held" {
		t.Fatalf("the other process did not take its lease: %q", line)
	}
	return command, stdin
}

func (m *toolsMachine) decideNow(t *testing.T, version string) {
	t.Helper()
	m.releases.Publish("auto-pigeon/ericw-tools", mt.Build(t, mt.Spec{Tool: managedtools.EricW, Version: version, Prerelease: true}))
	m.clock.advance(2 * time.Hour)
	m.step()
	_, body := m.call(http.MethodGet, "/api/v1/tools", nil)
	offer := offerIn(t, body, "ericw-tools")
	if status, answer := m.call(http.MethodPost, "/api/v1/tools/ericw-tools/decide", map[string]any{
		"decision": "now", "version": offer["version"], "asset_id": offer["asset_id"], "sha256": offer["sha256"]}); status != http.StatusOK {
		t.Fatalf("decide = %d: %v", status, answer["error"])
	}
}

// Another process's build (a `companion build run` in a terminal) holds the
// update; while the update waits, a new build here is refused retryably and
// starts nothing; once that build ends the update proceeds; a process that
// died holding its lease does not hold it forever.
func TestAnotherProcessesBuildHoldsTheCompilerUpdate(t *testing.T) {
	m := newToolsMachine(t)
	m.step()
	registry, _ := m.server.ToolRegistry()
	_, stdin := otherProcess(t, registry.Root(), BundleTool, string(managedtools.EricW))

	m.decideNow(t, "1.4540")
	m.step()
	if got := m.active(managedtools.EricW); got.Version != "1.4536" {
		t.Fatalf("activated %s while another process's build ran", got.Version)
	}
	tool := toolIn(t, m.server.toolsStatus().Status, managedtools.EricW)
	if tool.Pending == nil || !strings.Contains(strings.Join(tool.Pending.Waiting, " "), "test process") {
		t.Fatalf("the wait does not name the other process's build: %+v", tool.Pending)
	}
	// Writer preference: new work waits for the switch, not the other way.
	source := filepath.Join(m.content, "room.map")
	writeFixtureFile(t, source, "{ \"classname\" \"worldspawn\" }\n")
	status, refused := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline": "auto-pigeon.q1.fast-preview", "inputs": map[string]string{"source_map": source}})
	if status != http.StatusServiceUnavailable || refused["code"] != codeToolsUpdating {
		t.Fatalf("a build while the update waits = %d %v", status, refused)
	}
	if runs, _ := m.server.buildsDir(); runs != "" {
		if entries, _ := os.ReadDir(runs); len(entries) != 0 {
			t.Fatalf("the refused build left %d build directories", len(entries))
		}
	}

	stdin.Close()
	deadline := time.Now().Add(5 * time.Second)
	for m.step(); m.active(managedtools.EricW).Version != "1.4540"; m.step() {
		if time.Now().After(deadline) {
			t.Fatalf("the update did not proceed after the other build ended: %+v", toolIn(t, m.server.toolsStatus().Status, managedtools.EricW).Pending)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// A process killed while holding its lease.
	killed, _ := otherProcess(t, registry.Root(), BundleTool, string(managedtools.EricW))
	m.decideNow(t, "1.4541")
	m.step()
	if got := m.active(managedtools.EricW); got.Version != "1.4540" {
		t.Fatalf("activated %s while the other build was alive", got.Version)
	}
	_ = killed.Process.Kill()
	_ = killed.Wait()
	m.step()
	if got := m.active(managedtools.EricW); got.Version != "1.4541" {
		t.Fatalf("a dead process's lease still holds the update: %s %+v", got.Version, toolIn(t, m.server.toolsStatus().Status, managedtools.EricW).Pending)
	}
}

// A pipeline held between its first and second stage keeps the programs it
// started with — even if the bindings are rewritten under it by something
// outside the protocol (an older Companion sharing the folder) — and a
// program changed under it stops it rather than run.
func TestAPipelineKeepsTheProgramsItStartedWith(t *testing.T) {
	m := newToolsMachine(t)
	m.step()
	old := m.active(managedtools.EricW)
	gate := filepath.Join(t.TempDir(), "go")
	source := filepath.Join(m.content, "held.map")
	writeFixtureFile(t, source, "{ \"classname\" \"worldspawn\" }\n// AUCOM_HOLD_UNTIL "+gate+"\n")
	status, started := m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline": "auto-pigeon.q1.fast-preview", "inputs": map[string]string{"source_map": source}})
	if status != http.StatusAccepted {
		t.Fatalf("start = %d %v", status, started)
	}
	id := started["build"].(string)
	m.waitForStep(t, id, "running")

	// Rewrite every EricW binding to another program, as a writer that does
	// not take the gate would.
	elsewhere := filepath.Join(t.TempDir(), "vis")
	if err := os.WriteFile(elsewhere, []byte("#!/bin/sh\nexit 9\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := binding.Update(m.bindings, func(set *binding.Set) error {
		for _, profileID := range ericwProfiles {
			local, found := set.Find(profileID)
			if found {
				local.Executables["vis"], local.Executables["light"] = elsewhere, elsewhere
				_ = set.Put(local)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, gate, "go")
	manifest := m.waitForBuild(id)
	if manifest["state"] != "succeeded" {
		t.Fatalf("the held build: %v %v", manifest["state"], manifest["error"])
	}
	text := string(mustJSON(t, manifest))
	if strings.Contains(text, elsewhere) || !strings.Contains(text, old.Programs["vis"]) {
		t.Fatalf("a later stage did not run the program the build started with (old vis %s)", old.Programs["vis"])
	}

	// The same, with the frozen program itself changed under the build.
	gate2 := filepath.Join(t.TempDir(), "go2")
	writeFixtureFile(t, source, "{ \"classname\" \"worldspawn\" }\n// AUCOM_HOLD_UNTIL "+gate2+"\n")
	m.restoreManagedBindings(t)
	status, started = m.call(http.MethodPost, "/api/v1/build/runs", map[string]any{
		"pipeline": "auto-pigeon.q1.fast-preview", "inputs": map[string]string{"source_map": source}})
	if status != http.StatusAccepted {
		t.Fatalf("second start = %d %v", status, started)
	}
	id = started["build"].(string)
	m.waitForStep(t, id, "running")
	vis, _ := os.ReadFile(old.Programs["vis"])
	if err := os.WriteFile(old.Programs["vis"], append(vis, '\n'), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, gate2, "go")
	manifest = m.waitForBuild(id)
	if manifest["state"] == "succeeded" || !strings.Contains(string(mustJSON(t, manifest)), "not the program this build started with") {
		t.Fatalf("a changed program ran: %v %v", manifest["state"], manifest["error"])
	}
}

// restoreManagedBindings binds EricW back to the active installation.
func (m *toolsMachine) restoreManagedBindings(t *testing.T) {
	t.Helper()
	record := m.active(managedtools.EricW)
	if err := m.server.ManagedToolsHost().Activate(mustTool(managedtools.EricW), record); err != nil {
		t.Fatal(err)
	}
}

// waitForStep waits until the build's first step is in state.
func (m *toolsMachine) waitForStep(t *testing.T, id, state string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		_, body := m.call(http.MethodGet, "/api/v1/build/runs/"+id, nil)
		if manifest, ok := body["manifest"].(map[string]any); ok {
			if steps, ok := manifest["steps"].([]any); ok && len(steps) > 0 && steps[0].(map[string]any)["state"] == state {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("build %s never had a %s first step", id, state)
}
