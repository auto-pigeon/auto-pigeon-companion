package web

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/leakadapter"
)

// 01_NEW_368 §3: the Jobs list's `diagnostic` flag and Play latest agree, and
// neither moves when the normal pipeline is pinned as the leak test, unpinned
// and pinned again: they read what each build recorded.
func TestPinningANormalPipelineForLeakTestsDoesNotHideItsNormalBuilds(t *testing.T) {
	m := newMachine(t)
	m.signIn()
	backend := m.server.backendAddress()
	dir, err := m.server.buildsDir()
	if err != nil || backend == "" {
		t.Fatalf("builds %q %v backend %q", dir, err, backend)
	}
	save := func(id string, minute int, leak bool) {
		root := filepath.Join(dir, id)
		bsp := filepath.Join(root, "output", "bsp", "arena.bsp")
		if err := os.MkdirAll(filepath.Dir(bsp), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bsp, []byte("BSP "+id), 0o600); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(bsp)
		sum := sha256.Sum256(raw)
		digest, size := "sha256:"+hex.EncodeToString(sum[:]), int64(len(raw))
		finished := time.Date(2026, 10, 29, 9, minute, 0, 0, time.UTC)
		manifest := &build.Manifest{SchemaVersion: build.SchemaVersion, BuildID: id, State: job.Succeeded,
			EngineFamily: "quake1", Directory: root,
			Pipeline:  build.DocumentRef{ID: "auto-pigeon.q1.normal", Name: "Quake 1 normal"},
			StartedAt: finished.Add(-time.Minute), FinishedAt: finished,
			Inputs:  []build.FileRecord{{Name: "source_map", Source: &build.SourceRef{Backend: backend, AssetType: "map", AssetID: "arena", Revision: minute}}},
			Outputs: []build.FileRecord{{Name: "bsp", Role: "q1.bsp", Path: bsp, Size: size, SHA256: digest}},
			Steps: []build.Step{{ID: "compile", Capability: "q1.bsp.compile", State: job.Succeeded},
				{ID: "vis", Capability: "q1.bsp.vis", State: job.Succeeded}, {ID: "light", Capability: "q1.bsp.light", State: job.Succeeded}},
		}
		if leak {
			manifest.LeakTest = &leakadapter.Binding{Game: "quake1"}
		}
		if err := manifest.Save(root); err != nil {
			t.Fatal(err)
		}
	}
	save("20261029T090100Z-0000a001", 1, false)
	save("20261029T090200Z-0000b002", 2, true)
	save("20261029T090300Z-0000a003", 3, false)

	check := func(when string) {
		t.Helper()
		status, body := m.call(http.MethodGet, "/api/v1/build/latest?asset_id=arena&family=quake1", nil)
		items, _ := body["items"].([]any)
		if status != http.StatusOK || len(items) != 1 {
			t.Fatalf("%s: latest %d %v", when, status, body)
		}
		item := items[0].(map[string]any)
		if item["state"] != build.LatestPlayable || item["build_id"] != "20261029T090300Z-0000a003" || item["leak_tests"] != float64(1) {
			t.Fatalf("%s: latest %v", when, item)
		}
		status, body = m.call(http.MethodGet, "/api/v1/build/runs", nil)
		rows, _ := body["items"].([]any)
		if status != http.StatusOK || len(rows) != 3 {
			t.Fatalf("%s: runs %d %v", when, status, body)
		}
		for _, row := range rows {
			fields := row.(map[string]any)
			id := fields["manifest"].(map[string]any)["build_id"]
			if want := id == "20261029T090200Z-0000b002"; fields["diagnostic"] != want {
				t.Errorf("%s: %s diagnostic = %v, want %v", when, id, fields["diagnostic"], want)
			}
		}
	}
	check("before any pin")
	for _, pin := range []string{"auto-pigeon.q1.normal", "", "auto-pigeon.q1.normal"} {
		if status, body := m.call(http.MethodPost, "/api/v1/leak-test/pipelines", map[string]any{"game": "quake1", "pipeline": pin}); status != http.StatusOK {
			t.Fatalf("pin %q: %d %v", pin, status, body)
		}
		check("pinned " + pin)
	}
}
