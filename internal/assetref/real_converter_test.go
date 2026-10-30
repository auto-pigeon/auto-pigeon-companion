package assetref

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/converter"
)

// These run the REAL cmd/mapio through the REAL runner and resolver: the path a build and Build & Run
// share. A fake runner proves the Companion's half; only this proves the two halves agree.

const libraryModule = "github.com/auto-pigeon/auto-pigeon-libraries"

var realBinary = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "aucom-real-mapio-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, converter.ExecutableBase)
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	command := exec.Command("go", "build", "-ldflags", "-X main.version=1.test", "-o", path, "./cmd/mapio")
	command.Dir = filepath.Join("..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v\n%s", err, output)
	}
	return path, nil
})

// realRunner is a release bundle's layout around the real converter, with its digest listed.
func realRunner(t *testing.T) *converter.ProcessRunner {
	t.Helper()
	binary, err := realBinary()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	deps := filepath.Join(dir, converter.DependenciesDir)
	if err := os.MkdirAll(deps, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(deps, filepath.Base(binary))
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}
	digest, err := converter.FileDigest(target)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(map[string]any{"members": []map[string]string{
		{"path": converter.DependenciesDir + "/" + filepath.Base(binary), "sha256": digest}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deps, converter.BundleManifestName), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	resolver := &converter.Resolver{Dir: dir}
	runner, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if provenance := runner.Provenance(); !provenance.Verified || provenance.Program != converter.ExecutableBase {
		t.Fatalf("provenance = %+v", provenance)
	}
	return runner
}

// q3Fixture copies a Quake III document out of the exact library revision go.mod pins.
func q3Fixture(t *testing.T, name string) string {
	t.Helper()
	command := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", libraryModule)
	command.Dir = filepath.Join("..", "..")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(output)), "go", "mapio", "testdata", "q3map", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func releaseFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "build", "release-fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheRealConverterConvertsEveryGameThroughTheBuildsPath(t *testing.T) {
	runner := realRunner(t)
	for name, source := range map[string]string{
		"quake1": releaseFixture(t, "release-acceptance-1.5.apmap"),
		"quake2": releaseFixture(t, "release-acceptance-q2-1.5.apmap"),
		"quake3": q3Fixture(t, "q3004-room.apmap"),
	} {
		t.Run(name, func(t *testing.T) {
			out, _, conversions, err := ConvertAPMapInputsRecorded(context.Background(), runner, map[string]string{"source_map": source}, nil)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(out["source_map"])
			if err != nil || !strings.Contains(string(data), "worldspawn") {
				t.Fatalf("the .map: %v, %d bytes", err, len(data))
			}
			record := conversions["source_map"]
			if record.Game != name || record.Converter != converter.ExecutableBase || !record.ConverterVerified ||
				record.ConverterLibrary == "" || !strings.HasPrefix(record.SourceSHA256, "sha256:") {
				t.Fatalf("record = %+v", record)
			}
			if (record.Manifest != nil) != (name == "quake3") {
				t.Fatalf("%s: manifest = %+v", name, record.Manifest)
			}
			if name == "quake3" && record.Manifest.Format == "" {
				t.Fatalf("manifest = %+v", record.Manifest)
			}
		})
	}
}

func TestARealRefusalReachesThePersonInItsOwnWords(t *testing.T) {
	runner := realRunner(t)
	source := q3Fixture(t, "q3004-malicious-missing-material.apmap")
	_, _, _, err := ConvertAPMapInputsRecorded(context.Background(), runner, map[string]string{"source_map": source}, nil)
	if err == nil {
		t.Fatal("a refused map converted")
	}
	first := strings.Split(err.Error(), "\n")[0]
	if !strings.Contains(first, "apmap-to-q3map refused") {
		t.Fatalf("the sentence a person reads is %q", first)
	}
	if entries, _ := filepath.Glob(filepath.Join(filepath.Dir(source), "converted-source_map", "*.map")); len(entries) != 0 {
		t.Fatalf("a refused conversion wrote %v", entries)
	}
}

func TestConcurrentRealConversionsDoNotSeeEachOther(t *testing.T) {
	runner := realRunner(t)
	var group sync.WaitGroup
	failures := make(chan error, 8)
	for index := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			source := filepath.Join(t.TempDir(), fmt.Sprintf("map%d.apmap", index))
			data, err := os.ReadFile(filepath.Join("..", "..", "build", "release-fixtures", "release-acceptance-1.5.apmap"))
			if err == nil {
				err = os.WriteFile(source, data, 0o644)
			}
			if err == nil {
				_, _, _, err = ConvertAPMapInputsRecorded(context.Background(), runner, map[string]string{"m": source}, nil)
			}
			failures <- err
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Error(err)
		}
	}
}

// A cancelled conversion ends the real child, with no output and no process left behind.
func TestCancellingARealConversionStopsIt(t *testing.T) {
	runner := realRunner(t)
	runner.Grace = 2 * time.Second
	source := releaseFixture(t, "release-acceptance-1.5.apmap")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_, _, _, err := ConvertAPMapInputsRecorded(ctx, runner, map[string]string{"m": source}, nil)
	if err == nil {
		t.Fatal("a cancelled conversion succeeded")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "cancel") {
		t.Fatalf("err = %v", err)
	}
}
