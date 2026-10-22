package web

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// NEW_330 (found by NEW_331 with a released Companion): Build & Run said
// "Starting the game ✓ / Running ✓ / Finished" while the engine job had failed
// with exit 127 in a millisecond — QuakeSpasm could not load a shared library.
// The run submitted the engine job and called that a launch. An engine that
// cannot start is a failed run, at the stage that started it, naming the job.
func TestBuildAndRunFailsWhenTheEngineCannotStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in engine is a shell script")
	}
	m := newMachine(t)
	m.preparePlay(t)

	// An engine that exits as a missing shared library makes the loader exit.
	broken := filepath.Join(t.TempDir(), "engine that cannot start")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\necho 'error while loading shared libraries: libmad.so.0' >&2\nexit 127\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, engineProfile := m.call(http.MethodGet, "/api/v1/profiles/aucom.fixture.q1-engine", nil)
	if status, body := m.call(http.MethodPost, "/api/v1/profiles/aucom.fixture.q1-engine/bind", map[string]any{
		"executables": map[string]string{"engine": broken},
		"roots":       map[string]string{"game_root": m.gameRoot, "content_root": m.content},
		"approve":     true,
		"digest":      engineProfile["digest"],
	}); status != http.StatusOK {
		t.Fatalf("binding the engine = %d: %v", status, body["error"])
	}

	status, started := m.call(http.MethodPost, "/api/v1/play/runs", playBody(m))
	if status != http.StatusAccepted {
		t.Fatalf("starting = %d: %v", status, started["error"])
	}
	run := m.awaitPlay(t, started["id"].(string))

	if run["state"] != "failed" || run["failed_at"] != "launching" {
		t.Fatalf("the run ended %v at %v (%v); an engine that exited 127 at once did not start the game",
			run["state"], run["failed_at"], run["error"])
	}
	launch, _ := run["launch"].(map[string]any)
	jobID, _ := launch["job_id"].(string)
	if jobID == "" {
		t.Fatalf("the failed launch kept no engine job to open: %v", run["launch"])
	}
	message, _ := run["error"].(string)
	if !strings.Contains(message, jobID) || !strings.Contains(message, "127") {
		t.Errorf("the failure %q does not name the engine job %s and its exit status", message, jobID)
	}
	for _, stage := range run["stages"].([]any) {
		if stage.(map[string]any)["state"] == "running" {
			t.Errorf("a run whose engine never started recorded a Running stage: %v", run["stages"])
		}
	}
}
