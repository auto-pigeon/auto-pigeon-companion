package build

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/binding"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/job"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// A stand-in toolchain that behaves the way the real one does.
//
// The fixtures here are not "a program that exits 0". They reproduce the three
// behaviours that made the qualified EricW profile need new format members in
// the first place, because those are the behaviours the wiring has to survive:
//
//   - `compile` writes its portal file and its log *beside the output it was
//     given*, and writes no portal file when the map leaks;
//   - `vis` opens a sidecar of the same stem in the directory it was handed its
//     input in, and fails loudly when it is not there;
//   - `light` rewrites the file it was given and writes a companion file beside
//     it with a different extension.
//
// A fixture that took every file as an argument would pass whatever the staging
// rules were, and would have told us nothing.
//
// It is this test binary, re-executed. `go test` already built an executable
// for this platform, and a compiled fixture in the repository is not something
// anybody wants to maintain for six platforms.

const helperFlag = "-aucom-build-test-helper"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == helperFlag {
		os.Exit(helperMain(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func helperMain(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "helper: no mode")
		return 2
	}
	mode, rest := args[0], args[1:]
	switch mode {
	case "compile":
		// compile [--leak] [--no-portals] <source> <destination>
		leak, portals := false, true
		for len(rest) > 0 && strings.HasPrefix(rest[0], "--") {
			switch rest[0] {
			case "--leak":
				leak = true
			case "--no-portals":
				portals = false
			default:
				fmt.Fprintln(os.Stderr, "helper: unknown compile flag", rest[0])
				return 2
			}
			rest = rest[1:]
		}
		// `-wadpath <directory>`, the way the real qbsp takes it: the flag and
		// the directory are two argv elements, and the fixture reads the
		// directory so a test can prove the compiler was handed the right one.
		wadpath := ""
		if len(rest) >= 2 && rest[0] == "-wadpath" {
			wadpath = rest[1]
			rest = rest[2:]
		}
		if len(rest) != 2 {
			fmt.Fprintln(os.Stderr, "helper: compile takes a source and a destination")
			return 2
		}
		source, destination := rest[0], rest[1]
		if wadpath != "" {
			names, readErr := os.ReadDir(wadpath)
			if readErr != nil {
				fmt.Fprintln(os.Stderr, "helper: -wadpath", readErr)
				return 1
			}
			for _, name := range names {
				fmt.Println("wadpath holds", name.Name())
			}
		}
		contents, err := os.ReadFile(source)
		if err != nil {
			fmt.Fprintln(os.Stderr, "helper:", err)
			return 1
		}
		stem := strings.TrimSuffix(destination, filepath.Ext(destination))
		if err := os.WriteFile(stem+".log", []byte("compiled "+filepath.Base(source)+"\n"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "helper:", err)
			return 1
		}
		if leak {
			// The real qbsp under -leaktest: a point file, no BSP, no portal
			// file, and a non-zero status.
			fmt.Println("*** WARNING: Reached occupant at (0 0 0), no filling performed.")
			if err := os.WriteFile(stem+".pts", []byte("0 0 0\n"), 0o600); err != nil {
				fmt.Fprintln(os.Stderr, "helper:", err)
				return 1
			}
			fmt.Println("Aborting because the leak test was used.")
			return 1
		}
		if err := os.WriteFile(destination, append([]byte("BSP:"), contents...), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "helper:", err)
			return 1
		}
		if portals {
			if err := os.WriteFile(stem+".prt", []byte("PRT\n"), 0o600); err != nil {
				fmt.Fprintln(os.Stderr, "helper:", err)
				return 1
			}
		}
		fmt.Println("wrote", filepath.Base(destination))
	case "vis":
		if len(rest) != 1 {
			fmt.Fprintln(os.Stderr, "helper: vis takes one file")
			return 2
		}
		bsp := rest[0]
		portal := strings.TrimSuffix(bsp, filepath.Ext(bsp)) + ".prt"
		if _, err := os.Stat(portal); err != nil {
			fmt.Fprintf(os.Stderr, "LoadPortals: couldn't read %s\n", filepath.Base(portal))
			fmt.Fprintln(os.Stderr, "No vising performed.")
			return 1
		}
		if err := appendTo(bsp, "+VIS"); err != nil {
			fmt.Fprintln(os.Stderr, "helper:", err)
			return 1
		}
		if err := os.WriteFile("vis.log", []byte("vised\n"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "helper:", err)
			return 1
		}
		fmt.Println("visdatasize: 1")
	case "light":
		if len(rest) != 1 {
			fmt.Fprintln(os.Stderr, "helper: light takes one file")
			return 2
		}
		bsp := rest[0]
		if err := appendTo(bsp, "+LIGHT"); err != nil {
			fmt.Fprintln(os.Stderr, "helper:", err)
			return 1
		}
		companion := strings.TrimSuffix(bsp, filepath.Ext(bsp)) + ".lit"
		if err := os.WriteFile(companion, []byte("LIT\n"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "helper:", err)
			return 1
		}
		if err := os.WriteFile("light.log", []byte("lit\n"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "helper:", err)
			return 1
		}
		fmt.Println("lightdatasize: 1")
	case "warn":
		// Exits zero having said something a rule classifies as an error, which
		// is what `--strict` exists for.
		fmt.Println("*** WARNING: something is wrong and I am carrying on anyway")
		if len(rest) == 2 {
			if err := os.WriteFile(rest[1], []byte("BSP\n"), 0o600); err != nil {
				fmt.Fprintln(os.Stderr, "helper:", err)
				return 1
			}
			stem := strings.TrimSuffix(rest[1], filepath.Ext(rest[1]))
			_ = os.WriteFile(stem+".prt", []byte("PRT\n"), 0o600)
			_ = os.WriteFile(stem+".log", []byte("log\n"), 0o600)
		}
	case "sleep":
		fmt.Println("sleeping")
		os.Stdout.Sync()
		time.Sleep(60 * time.Second)
	default:
		fmt.Fprintln(os.Stderr, "helper: unknown mode", mode)
		return 2
	}
	return 0
}

func appendTo(path, text string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(text); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func helperPath(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}
	return path
}

// The fixture documents.
//
// Written as JSON and decoded through the real decoder, for the reason the
// executor's fixtures are: a fixture built as Go values would exercise a path
// no document a user writes can take.

// fixtureTool is a three-stage toolchain over the helper. `mode` selects which
// helper behaviour the compile stage has.
func fixtureTool(t *testing.T, id string, capabilityPrefix string, compileMode string) []byte {
	t.Helper()
	capability := func(name string) string { return capabilityPrefix + "." + name }
	// Every action's argv starts with the flag that selects the helper, because
	// the "tool" is this test binary and without it the binary would run the
	// whole suite again — recursively, once per stage. It is an *argument* and
	// not an environment variable for the reason the executor's own fixtures
	// give: the executor deliberately does not pass its environment to a job,
	// and a fixture that depended on it would be testing around the thing.
	var compileArgs []any
	switch compileMode {
	case "compile-leak":
		compileArgs = []any{helperFlag, "compile", "--leak"}
	case "compile-no-portals":
		compileArgs = []any{helperFlag, "compile", "--no-portals"}
	default:
		compileArgs = []any{helperFlag, compileMode}
	}
	compileArgs = append(compileArgs,
		map[string]any{"value": "-wadpath", "when": map[string]any{"root": "content_root"}},
		map[string]any{"value": "{root.content_root}", "when": map[string]any{"root": "content_root"}},
		"{input.source_map}", "{output.bsp}")

	document := map[string]any{
		"schema_version": "aucom.profile/1.1",
		"kind":           "tool",
		"id":             id,
		"version":        "1.0.0",
		"name":           "Build test toolchain",
		"summary":        "A fixture toolchain that behaves the way the real Quake 1 compilers behave.",
		"publisher":      map[string]any{"name": "Auto-Pigeon tests"},
		"license":        map[string]any{"spdx": "MIT", "name": "MIT"},
		"tool_version":   "0.0.0-fixture",
		"platforms": []map[string]any{
			{"platform": map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH}, "status": "supported"},
		},
		"acquisition": []map[string]any{
			{"mode": "user_path", "title": "Point at a copy you already have", "hint": "choose the test binary"},
		},
		"capabilities": []map[string]any{
			{"id": capability("compile"), "title": "Compile", "consumes": []string{"test.map"}, "produces": []string{"test.bsp", "test.prt"}},
			{"id": capability("vis"), "title": "Vis", "consumes": []string{"test.bsp"}, "produces": []string{"test.bsp.vised"}},
			{"id": capability("light"), "title": "Light", "consumes": []string{"test.bsp.vised"}, "produces": []string{"test.bsp.lit", "test.lit"}},
		},
		"executables": []map[string]any{
			{"name": "tool", "title": "The fixture program", "file": "tool{platform.exe_suffix}"},
		},
		"actions": []map[string]any{
			{
				"id": "compile", "title": "Compile", "capability": capability("compile"),
				"executable": "tool", "args": compileArgs,
				"working_dir": map[string]any{"root": "workspace"},
				"inputs": []map[string]any{
					{"name": "source_map", "title": "Source", "role": "test.map", "required": true, "extensions": []string{".map"}},
				},
				"outputs": []map[string]any{
					{"name": "bsp", "title": "BSP", "role": "test.bsp", "path": "{option.basename}.bsp"},
					{"name": "prt", "title": "Portals", "role": "test.prt", "path": "{option.basename}.prt", "optional": true},
					{"name": "pts", "title": "Point file", "role": "test.pts", "path": "{option.basename}.pts", "optional": true},
					{"name": "log", "title": "Log", "role": "test.compile.log", "path": "{option.basename}.log", "optional": true},
				},
				"options": []map[string]any{
					{"name": "basename", "title": "Name", "type": "text", "default": "level", "max_length": 64},
				},
				"diagnostics": []map[string]any{
					{"id": "leak", "match": "Reached occupant", "severity": "error", "message": "The map leaks."},
					{"id": "warned", "match": "*** WARNING", "severity": "error", "message": "The compiler warned about something."},
				},
				"roots": []map[string]any{
					{"role": "workspace", "access": "read_write", "purpose": "compile"},
					// The EricW `-wadpath` shape: an optional read-only content
					// root, passed only when one is set. Declared on the
					// compile action alone, exactly as the real Q1 profile
					// declares it, so a test can prove the later stages do not
					// receive it.
					{"role": "content_root", "access": "read", "optional": true, "purpose": "find the texture WADs the map names"},
				},
				"timeout_seconds": 120,
			},
			{
				"id": "vis", "title": "Vis", "capability": capability("vis"),
				"executable": "tool", "args": []any{helperFlag, "vis", "{input.bsp}"},
				"working_dir": map[string]any{"root": "workspace"},
				"inputs": []map[string]any{
					{"name": "bsp", "title": "BSP", "role": "test.bsp", "required": true, "extensions": []string{".bsp"}},
					{"name": "prt", "title": "Portals", "role": "test.prt", "required": true, "extensions": []string{".prt"}, "stage_with": "bsp"},
				},
				"outputs": []map[string]any{
					{"name": "bsp", "title": "Vised BSP", "role": "test.bsp.vised", "in_place": "bsp"},
					{"name": "log", "title": "Log", "role": "test.vis.log", "path": "vis.log", "optional": true},
				},
				"diagnostics": []map[string]any{
					{"id": "no_portals", "match": "couldn't read", "severity": "error", "message": "No portal file."},
				},
				"roots":           []map[string]any{{"role": "workspace", "access": "read_write", "purpose": "vis"}},
				"timeout_seconds": 120,
			},
			{
				"id": "light", "title": "Light", "capability": capability("light"),
				"executable": "tool", "args": []any{helperFlag, "light", "{input.bsp}"},
				"working_dir": map[string]any{"root": "workspace"},
				"inputs": []map[string]any{
					{"name": "bsp", "title": "Vised BSP", "role": "test.bsp.vised", "required": true, "extensions": []string{".bsp"}},
				},
				"outputs": []map[string]any{
					{"name": "bsp", "title": "Lit BSP", "role": "test.bsp.lit", "in_place": "bsp"},
					{"name": "lit", "title": "Coloured light", "role": "test.lit", "in_place": "bsp", "extension": ".lit", "optional": true},
					{"name": "log", "title": "Log", "role": "test.light.log", "path": "light.log", "optional": true},
				},
				"roots":           []map[string]any{{"role": "workspace", "access": "read_write", "purpose": "light"}},
				"timeout_seconds": 120,
			},
		},
	}
	return encode(t, document)
}

// fixturePipeline is the three stages wired end to end, including the sidecar.
func fixturePipeline(t *testing.T, id string, capabilityPrefix string) []byte {
	t.Helper()
	capability := func(name string) string { return capabilityPrefix + "." + name }
	document := map[string]any{
		"schema_version": "aucom.profile/1.1",
		"kind":           "pipeline",
		"id":             id,
		"version":        "1.0.0",
		"name":           "Build test pipeline",
		"summary":        "Three stages with a sidecar wire, an in-place rewrite and a companion file.",
		"publisher":      map[string]any{"name": "Auto-Pigeon tests"},
		"license":        map[string]any{"spdx": "MIT", "name": "MIT"},
		"inputs": []map[string]any{
			{"name": "source_map", "title": "Source", "role": "test.map", "required": true, "extensions": []string{".map"}},
		},
		"steps": []map[string]any{
			{"id": "compile", "title": "Compile", "capability": capability("compile"),
				"inputs": []map[string]any{{"name": "source_map", "from": "pipeline.source_map"}}},
			{"id": "vis", "title": "Vis", "capability": capability("vis"),
				"inputs": []map[string]any{
					{"name": "bsp", "from": "compile.bsp"},
					{"name": "prt", "from": "compile.prt"},
				}},
			{"id": "light", "title": "Light", "capability": capability("light"),
				"inputs": []map[string]any{{"name": "bsp", "from": "vis.bsp"}}},
		},
		"outputs": []map[string]any{
			{"name": "bsp", "title": "The BSP", "role": "test.bsp.lit", "from": "light.bsp"},
			{"name": "lit", "title": "Coloured light", "role": "test.lit", "from": "light.lit", "optional": true},
			{"name": "pts", "title": "Point file", "role": "test.pts", "from": "compile.pts", "optional": true},
			{"name": "compile_log", "title": "Compile log", "role": "test.compile.log", "from": "compile.log", "optional": true},
		},
	}
	return encode(t, document)
}

func encode(t *testing.T, document map[string]any) []byte {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding a fixture: %v", err)
	}
	return encoded
}

// harness is a runner wired to temporary directories, with the fixture profiles
// installed, granted and bound to the test binary.
type harness struct {
	t       *testing.T
	runner  *Runner
	service *job.Service
	dir     string
	builds  string
	project string
	cancel  context.CancelFunc
}

func newHarness(t *testing.T, documents map[string][]byte) *harness {
	return newHarnessKeeping(t, documents, false)
}

// newHarnessKeeping is newHarness with the job workspaces left behind, so a test
// can inspect where the executor actually staged a file.
func newHarnessKeeping(t *testing.T, documents map[string][]byte, keep bool) *harness {
	t.Helper()
	dir := t.TempDir()
	profiles := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profiles, 0o700); err != nil {
		t.Fatalf("%v", err)
	}
	for name, document := range documents {
		if err := os.WriteFile(filepath.Join(profiles, name), document, 0o600); err != nil {
			t.Fatalf("%v", err)
		}
	}
	store, err := job.OpenStore(filepath.Join(dir, "jobs"))
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	catalog := job.NewCatalog(profiles)

	// Real grants, against the real digests, through the real Authorize. A
	// harness that bypassed authorization would leave every fixture below
	// testing a path no installed profile takes.
	bindings := map[string]binding.LocalBinding{}
	entries, err := catalog.List()
	if err != nil {
		t.Fatalf("reading the fixture catalog: %v", err)
	}
	for _, entry := range entries {
		tool, isTool := entry.Profile.(*profile.ToolProfile)
		if !isTool {
			continue
		}
		meta := entry.Profile.Metadata()
		bindings[meta.ID] = binding.LocalBinding{
			SchemaVersion: binding.SchemaVersion,
			ProfileID:     meta.ID,
			ProfileDigest: entry.Digest,
			Trust:         entry.Trust,
			Acquisition:   profile.AcquireUserPath,
			Executables:   map[string]string{"tool": helperPath(t)},
			Grant: &profile.Grant{
				ProfileID: meta.ID,
				Version:   meta.Version,
				Digest:    entry.Digest,
				Trust:     entry.Trust,
				Granted:   profile.PermissionIDs(tool),
				GrantedAt: time.Now().UTC(),
			},
		}
	}
	lookup := func(id string) (binding.LocalBinding, bool) {
		local, found := bindings[id]
		return local, found
	}

	service, err := job.NewService(job.Options{
		Store:         store,
		Catalog:       catalog,
		Bindings:      lookup,
		Concurrency:   2,
		KeepWorkspace: keep,
		Logf:          func(format string, args ...any) { t.Logf("service: "+format, args...) },
	})
	if err != nil {
		t.Fatalf("building the service: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := service.Start(ctx); err != nil {
		cancel()
		t.Fatalf("starting the service: %v", err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("closing the service: %v", err)
		}
		cancel()
	})

	builds := filepath.Join(dir, "builds")
	runner, err := New(Options{
		Service:   service,
		Dir:       builds,
		Bindings:  lookup,
		Companion: "0.0.0-test",
		Logf:      func(format string, args ...any) { t.Logf("build: "+format, args...) },
	})
	if err != nil {
		t.Fatalf("building the runner: %v", err)
	}

	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatalf("%v", err)
	}
	return &harness{t: t, runner: runner, service: service, dir: dir, builds: builds, project: project, cancel: cancel}
}

// standardFixtures is the ordinary three-stage toolchain and its pipeline.
func standardFixtures(t *testing.T) map[string][]byte {
	return map[string][]byte{
		"tool.tool.json":         fixtureTool(t, "test.build.toolchain", "test.stage", "compile"),
		"pipeline.pipeline.json": fixturePipeline(t, "test.build.pipeline", "test.stage"),
	}
}

// sourceMap writes a map source for the build to consume.
func (h *harness) sourceMap(name, contents string) string {
	h.t.Helper()
	path := filepath.Join(h.project, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		h.t.Fatalf("%v", err)
	}
	return path
}

func (h *harness) run(request Request) (*Manifest, error) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	return h.runner.Run(ctx, request)
}

func (h *harness) mustRun(request Request) *Manifest {
	h.t.Helper()
	manifest, err := h.run(request)
	if err != nil {
		h.t.Fatalf("the build failed: %v", err)
	}
	return manifest
}

func stepNamed(t *testing.T, manifest *Manifest, id string) Step {
	t.Helper()
	for _, step := range manifest.Steps {
		if step.ID == id {
			return step
		}
	}
	t.Fatalf("the manifest has no %q step", id)
	return Step{}
}

func outputNamed(t *testing.T, records []FileRecord, name string) FileRecord {
	t.Helper()
	for _, record := range records {
		if record.Name == name {
			return record
		}
	}
	t.Fatalf("there is no %q among %d records", name, len(records))
	return FileRecord{}
}
