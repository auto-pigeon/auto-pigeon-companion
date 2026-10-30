package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cubeMap = `{
"classname" "worldspawn"
{
( -64 -64 -16 ) ( -64 -63 -16 ) ( -64 -64 -15 ) base 0 0 0 1 1
( -64 -64 -16 ) ( -64 -64 -15 ) ( -63 -64 -16 ) base 0 0 0 1 1
( -64 -64 -16 ) ( -63 -64 -16 ) ( -64 -63 -16 ) base 0 0 0 1 1
( 64 64 16 ) ( 64 65 16 ) ( 65 64 16 ) base 0 0 0 1 1
( 64 64 16 ) ( 65 64 16 ) ( 64 64 17 ) base 0 0 0 1 1
( 64 64 16 ) ( 64 64 17 ) ( 64 65 16 ) base 0 0 0 1 1
}
}
`

func invoke(t *testing.T, ctx context.Context, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(ctx, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func apmapOf(t *testing.T, directory string) string {
	t.Helper()
	source := filepath.Join(directory, "cube.map")
	if err := os.WriteFile(source, []byte(cubeMap), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(directory, "apmap")
	code, _, stderr := invoke(t, context.Background(), "convert", "--q1map-to-apmap", "--input", source, "--output", out)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	return filepath.Join(out, "cube.apmap")
}

func TestTheProtocolNamesThisProgramAndItsLicences(t *testing.T) {
	code, stdout, stderr := invoke(t, context.Background(), "protocol", "--json")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var document protocolDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatal(err)
	}
	if document.Program != Program || document.Protocol != Protocol || document.SchemaVersion != SchemaVersion {
		t.Fatalf("identity: %+v", document)
	}
	if strings.Contains(stdout, "auto-pigeon-extractor") {
		t.Fatalf("the protocol document names the extractor:\n%s", stdout)
	}
	if document.License.SPDX != "Apache-2.0 AND MIT" {
		t.Fatalf("licence: %+v", document.License)
	}
	if document.Library.Module != "github.com/auto-pigeon/auto-pigeon-libraries" {
		t.Fatalf("library: %+v", document.Library)
	}
	for _, want := range []string{"apmap-to-q1map", "apmap-to-q2map", "apmap-to-q3map"} {
		if !strings.Contains(strings.Join(document.Capabilities.Directions, " "), want) {
			t.Fatalf("%s is not advertised: %+v", want, document.Capabilities)
		}
	}
	if document.Capabilities.APMapCurrent == "" || len(document.Capabilities.APMapReadable) < 2 {
		t.Fatalf("capabilities: %+v", document.Capabilities)
	}
}

func TestAConversionWritesItsOutputAndReportsIt(t *testing.T) {
	directory := t.TempDir()
	source := apmapOf(t, directory)
	out := filepath.Join(directory, "out")
	code, stdout, stderr := invoke(t, context.Background(), "convert", "--apmap-to-q1map", "--input", source, "--output", out, "--json")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var result convertResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if result.Output != filepath.Join(out, "cube.map") || !result.Validated || result.Warnings == nil {
		t.Fatalf("result: %+v", result)
	}
	written, err := os.ReadFile(result.Output)
	if err != nil || !strings.Contains(string(written), "worldspawn") {
		t.Fatalf("output: %q, %v", written, err)
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the output directory holds %v (%v); a temporary file was left", entries, err)
	}
}

func TestARefusalIsATerminalRecordAndWritesNothing(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "broken.apmap")
	if err := os.WriteFile(source, []byte(`{"apmap_version": "0.0", "game": "quake1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(directory, "out")
	code, _, stderr := invoke(t, context.Background(), "convert", "--apmap-to-q1map", "--input", source, "--output", out)
	if code != exitFailed {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, TerminalMarker) {
		t.Fatalf("the last stderr line is not the terminal record:\n%s", stderr)
	}
	var record map[string]string
	if err := json.Unmarshal([]byte(strings.TrimPrefix(last, TerminalMarker)), &record); err != nil {
		t.Fatal(err)
	}
	if record["reason"] != "unsupported_version" || !strings.Contains(record["detail"], "0.0") {
		t.Fatalf("record: %+v", record)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("a refused conversion created its output directory: %v", err)
	}
}

func TestACancelledConversionSaysSoAndWritesNothing(t *testing.T) {
	directory := t.TempDir()
	source := apmapOf(t, directory)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := filepath.Join(directory, "out")
	code, _, stderr := invoke(t, ctx, "convert", "--apmap-to-q1map", "--input", source, "--output", out)
	if code != exitFailed || !strings.Contains(stderr, `"reason":"cancelled"`) {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "cube.map")); !os.IsNotExist(err) {
		t.Fatalf("a cancelled conversion wrote: %v", err)
	}
}

func TestAWrongInvocationIsAUsageError(t *testing.T) {
	directory := t.TempDir()
	source := apmapOf(t, directory)
	for name, args := range map[string][]string{
		"nothing":        {},
		"unknown":        {"extract-stairs"},
		"no direction":   {"convert", "--input", source, "--output", directory},
		"two directions": {"convert", "--apmap-to-q1map", "--apmap-to-q2map", "--input", source, "--output", directory},
		"no output":      {"convert", "--apmap-to-q1map", "--input", source},
		"stray argument": {"convert", "--apmap-to-q1map", "--input", source, "--output", directory, "extra"},
		"protocol text":  {"protocol"},
	} {
		if code, _, _ := invoke(t, context.Background(), args...); code != exitUsage {
			t.Errorf("%s: exit %d, want %d", name, code, exitUsage)
		}
	}
}

func TestAnUnreadableInputIsNamed(t *testing.T) {
	directory := t.TempDir()
	code, _, stderr := invoke(t, context.Background(), "convert", "--apmap-to-q1map", "--input", directory, "--output", filepath.Join(directory, "out"))
	if code != exitFailed || !strings.Contains(stderr, `"reason":"io_error"`) || !strings.Contains(stderr, "not a regular file") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
}

func TestWriteAllLeavesNothingWhenAWriteFails(t *testing.T) {
	directory := t.TempDir()
	blocked := filepath.Join(directory, "blocked")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	// A directory where a file must land makes the rename fail after the first file could have gone.
	err := writeAll(directory, map[string][]byte{blocked: []byte("x")})
	if err == nil {
		t.Fatal("writing over a directory succeeded")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("left behind: %v (%v)", entries, err)
	}
}
