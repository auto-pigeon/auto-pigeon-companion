// Command auto-pigeon-mapio is the small map converter a Companion release ships beside the Companion.
//
// It is a wrapper and nothing else: argument handling, file I/O, process signals and a machine-readable
// result around go/mapio from auto-pigeon-libraries (Apache-2.0), which owns the formats. This file is
// MIT, like the rest of this repository; the executable built from it contains both, and its protocol
// document says so. It has no format code, runs no other program and reads no mapper configuration.
//
//	auto-pigeon-mapio version [--json]
//	auto-pigeon-mapio protocol --json
//	auto-pigeon-mapio convert --<direction> --input <file> --output <dir> [--json]
//
// Exit 0 is a conversion that published its whole result, 1 a conversion that did not, and 2 an
// invocation that was wrong. commit.go says exactly what each of those leaves in the output directory. A
// failed conversion's last stderr line is a terminal record, which is how a supervisor reads the reason
// without parsing prose.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/auto-pigeon/auto-pigeon-libraries/go/mapio"
)

// Program is this executable's identity. A supervisor checks it, so a different program dropped at
// this path cannot pass the handshake by speaking the same protocol number.
const Program = "auto-pigeon-mapio"

// Protocol is the invocation contract: the commands, their flags, the JSON documents and the exit
// codes below. A later minor only adds; a later major is a different contract.
const Protocol = "1.0"

// SchemaVersion names the shape of the protocol document itself.
const SchemaVersion = "auto-pigeon-mapio-protocol/1.0"

// License is the executable's licence: the go/mapio library it links and this wrapper. Each part's
// text is in the notices file a release ships beside the executable.
const License = "Apache-2.0 AND MIT"

// Source says where each part of the executable is developed.
const Source = "https://github.com/auto-pigeon/auto-pigeon-companion (cmd/mapio, MIT) and " +
	"https://github.com/auto-pigeon/auto-pigeon-libraries (go/mapio, Apache-2.0)"

// TerminalMarker begins the last stderr line of a failed conversion.
const TerminalMarker = "MAPIO-TERMINAL/1.0 "

// manifestSuffix is what go/mapio names a Quake III conversion's manifest after the `.map`'s stem.
const manifestSuffix = ".q3map-manifest.json"

// DeadlineEnv is the whole-run deadline a supervisor may set, in seconds, so a run ends itself with a
// named reason rather than being killed with none.
const DeadlineEnv = "AUTO_PIGEON_JOB_DEADLINE_SECONDS"

// maxInputBytes bounds what one conversion reads into memory.
const maxInputBytes = 1 << 30

const (
	exitOK     = 0
	exitFailed = 1
	exitUsage  = 2
)

// version is stamped by build/release.sh, as the Companion's is.
var version = "unknown"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "protocol":
		return runProtocol(args[1:], stdout, stderr)
	case "convert":
		return runConvert(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	}
	fmt.Fprintf(stderr, "error: %s is not a command\n", args[0])
	usage(stderr)
	return exitUsage
}

func usage(out io.Writer) {
	fmt.Fprintln(out, "usage:")
	fmt.Fprintln(out, "  "+Program+" version [--json]")
	fmt.Fprintln(out, "  "+Program+" protocol --json")
	fmt.Fprintln(out, "  "+Program+" convert --<direction> --input <file> --output <dir> [--json]")
	fmt.Fprintln(out, "directions:")
	for _, direction := range mapio.Directions() {
		fmt.Fprintf(out, "  --%-16s %s\n", direction.Name, direction.Summary)
	}
}

// protocolDocument is what `protocol --json` prints. Members are only ever added within a major.
type protocolDocument struct {
	SchemaVersion string         `json:"schema_version"`
	Program       string         `json:"program"`
	Protocol      string         `json:"protocol"`
	Version       string         `json:"version"`
	License       license        `json:"license"`
	Library       library        `json:"library"`
	Capabilities  capabilities   `json:"capabilities"`
	ExitCodes     map[int]string `json:"exit_codes"`
}

type license struct {
	SPDX   string `json:"spdx"`
	Source string `json:"source"`
}

type library struct {
	Module  string `json:"module"`
	Version string `json:"version"`
}

type capabilities struct {
	Directions     []string `json:"directions"`
	APMapCurrent   string   `json:"apmap_current"`
	APMapReadable  []string `json:"apmap_readable"`
	ContractSource string   `json:"contract_source"`
}

func describe() (protocolDocument, error) {
	found, err := mapio.CapabilitiesOf(nil)
	if err != nil {
		return protocolDocument{}, err
	}
	names := make([]string, 0, len(found.Directions))
	for _, direction := range found.Directions {
		names = append(names, direction.Name)
	}
	return protocolDocument{
		SchemaVersion: SchemaVersion, Program: Program, Protocol: Protocol, Version: version,
		License: license{SPDX: License, Source: Source},
		Library: library{Module: mapio.ModulePath, Version: mapio.BuildModuleVersion()},
		Capabilities: capabilities{Directions: names, APMapCurrent: found.APMapCurrent,
			APMapReadable: found.APMapReadable, ContractSource: found.ContractSource},
		ExitCodes: map[int]string{exitOK: "the conversion wrote its output", exitFailed: "the conversion did not",
			exitUsage: "the invocation was wrong"},
	}, nil
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "print the version as JSON")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return exitUsage
	}
	document, err := describe()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitFailed
	}
	if *asJSON {
		return writeJSON(stdout, stderr, map[string]string{
			"program": Program, "version": version, "library_version": document.Library.Version,
			"apmap_current": document.Capabilities.APMapCurrent, "license": License,
		})
	}
	fmt.Fprintf(stdout, "%s %s (go/mapio %s, APMap %s, protocol %s, %s)\n", Program, version,
		document.Library.Version, document.Capabilities.APMapCurrent, Protocol, License)
	return exitOK
}

func runProtocol(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("protocol", flag.ContinueOnError)
	flags.SetOutput(stderr)
	asJSON := flags.Bool("json", false, "print the protocol document")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !*asJSON {
		fmt.Fprintln(stderr, "usage: "+Program+" protocol --json")
		return exitUsage
	}
	document, err := describe()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitFailed
	}
	return writeJSON(stdout, stderr, document)
}

// convertResult is what `convert --json` prints. It holds no duration and no absolute path of the
// machine beyond the ones it was given, so it is a pure function of the invocation.
type convertResult struct {
	Direction string          `json:"direction"`
	Input     string          `json:"input"`
	Output    string          `json:"output"`
	Manifest  string          `json:"manifest,omitempty"`
	Validated bool            `json:"validated"`
	Warnings  []mapio.Warning `json:"warnings"`
}

func runConvert(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("convert", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "the file to convert")
	output := flags.String("output", "", "the directory to write into")
	asJSON := flags.Bool("json", false, "print the result as JSON")
	chosen := map[string]*bool{}
	for _, direction := range mapio.Directions() {
		chosen[direction.Name] = flags.Bool(direction.Name, false, direction.Summary)
	}
	if flags.Parse(args) != nil {
		return exitUsage
	}
	var selected []string
	for name, set := range chosen {
		if *set {
			selected = append(selected, name)
		}
	}
	if len(selected) != 1 || *input == "" || *output == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "error: convert needs exactly one --<direction>, --input and --output")
		usage(stderr)
		return exitUsage
	}
	direction, _ := mapio.LookupDirection(selected[0])

	if seconds, ok := deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
		defer cancel()
	}

	data, err := readInput(*input)
	if err != nil {
		return terminal(stderr, "io_error", "the input could not be read", err.Error())
	}
	// The strict policy: what a compiler is handed has been checked. There is no flag to turn it off.
	converted, result, err := mapio.Convert(ctx, direction, *input, data, mapio.Options{Validate: true})
	if err != nil {
		refusal := mapio.RefusalOf(err)
		message := direction.Name + " refused"
		if refusal.Detail != "" {
			message += " [" + refusal.Detail + "]"
		}
		return terminal(stderr, string(refusal.Code), message, refusal.Message)
	}

	outputPath := filepath.Join(*output, result.OutputName)
	files := []resultFile{{Name: result.OutputName, Data: converted}}
	manifestPath := ""
	if result.ManifestName != "" {
		manifestPath = filepath.Join(*output, result.ManifestName)
		files = append(files, resultFile{Name: result.ManifestName, Data: result.ManifestBytes})
	} else if stem, isMap := strings.CutSuffix(result.OutputName, ".map"); isMap {
		// A `.map` of another game replacing a Quake III one: its manifest would describe bytes that
		// are no longer there.
		files = append(files, resultFile{Name: stem + manifestSuffix, Retire: true})
	}
	if err := commitResult(ctx, *output, files); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return terminal(stderr, string(mapio.RefusalOf(err).Code), "the conversion was stopped before its output was published", err.Error())
		}
		return terminal(stderr, "io_error", "the output could not be written", err.Error())
	}

	warnings := result.Warnings
	if warnings == nil {
		warnings = []mapio.Warning{}
	}
	if *asJSON {
		return writeJSON(stdout, stderr, convertResult{Direction: direction.Name, Input: *input, Output: outputPath,
			Manifest: manifestPath, Validated: result.Validated, Warnings: warnings})
	}
	for _, warning := range warnings {
		fmt.Fprintln(stderr, "warning: "+warning.Text)
	}
	fmt.Fprintln(stdout, outputPath)
	return exitOK
}

// deadline reads the supervisor's whole-run deadline. A value that is not a positive number sets none:
// the supervisor still enforces its own.
func deadline() (int64, bool) {
	seconds, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(DeadlineEnv)), 10, 64)
	return seconds, err == nil && seconds > 0
}

func readInput(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > maxInputBytes {
		return nil, fmt.Errorf("%s is %d bytes; this converter reads up to %d", path, info.Size(), int64(maxInputBytes))
	}
	return os.ReadFile(path)
}

// terminal writes the record a supervisor reads, as the last stderr line, and fails the run.
func terminal(stderr io.Writer, reason, message, detail string) int {
	record, err := json.Marshal(map[string]string{"reason": reason, "message": message, "detail": detail})
	if err != nil {
		fmt.Fprintf(stderr, "error: %s: %s\n", message, detail)
		return exitFailed
	}
	fmt.Fprintf(stderr, "error: %s: %s\n", message, detail)
	fmt.Fprintf(stderr, "%s%s\n", TerminalMarker, record)
	return exitFailed
}

func writeJSON(stdout, stderr io.Writer, document any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(document); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitFailed
	}
	return exitOK
}
