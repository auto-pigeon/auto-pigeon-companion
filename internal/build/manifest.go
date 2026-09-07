package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/binding"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/job"
	"github.com/andrea-dintino/auto-pigeon-companion/internal/profile"
)

// SchemaVersion versions the build manifest.
//
// A published record, unlike a job record: a manifest travels with a BSP, is
// what somebody else reads to find out what produced it, and may be read by a
// build of the Companion older or newer than the one that wrote it. So it is
// versioned by name and refused rather than half-read.
const SchemaVersion = "aucom.build-manifest/1.0"

// ManifestFileName is what the manifest is called inside a build directory.
const ManifestFileName = "manifest.json"

// DocumentRef identifies one profile document exactly.
type DocumentRef struct {
	ID      string        `json:"id"`
	Version string        `json:"version"`
	Name    string        `json:"name,omitempty"`
	Digest  string        `json:"digest"`
	Trust   profile.Trust `json:"trust,omitempty"`
}

// FileRecord is one file the build read or wrote.
//
// The digest is the point. A path is where a file was on one machine at one
// moment; a digest is what the file was, and it is the only half of this that
// means anything to somebody reading the manifest somewhere else.
type FileRecord struct {
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
	Path string `json:"path,omitempty"`
	// From is the wire this file arrived on, for a step input: `pipeline.<name>`
	// or `<step>.<output>`.
	From   string `json:"from,omitempty"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	// Missing records a declared file that was not produced. Optional ones are
	// a fact; a required one is why the build failed.
	Missing  bool `json:"missing,omitempty"`
	Optional bool `json:"optional,omitempty"`
}

// ExecutableRecord is one program a step actually started.
type ExecutableRecord struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	// Unreadable says why there is no digest, when there is none. A missing
	// digest with no reason beside it reads as "nobody bothered".
	Unreadable string `json:"unreadable,omitempty"`
}

// ToolRecord is one tool profile a build used, and what version of the program
// behind it ran.
//
// # Why there is no version string from the program itself
//
// A tool profile may declare a `version_probe`, and running it would mean
// starting a process outside the job executor — which ADR-0003 says nothing in
// this repository does. So the question "which version ran" is answered by
// evidence instead: the digest of the executable that was started, the
// catalogue package and version it was installed from, and that package's
// artifact digest, which a signed catalogue vouched for. That is a stronger
// answer than a banner string, and it is one nobody can print.
type ToolRecord struct {
	Profile DocumentRef `json:"profile"`
	// ToolVersion is the upstream version the *document* describes.
	ToolVersion string `json:"tool_version,omitempty"`
	// ResolvedVersion is what a version probe reported when the tool was bound,
	// if one ever was. Empty is normal and honest.
	ResolvedVersion string `json:"resolved_version,omitempty"`
	// Acquisition is how the executables got onto this machine.
	Acquisition profile.AcquisitionMode `json:"acquisition,omitempty"`
	// Installs is the managed downloads this profile is pinned to.
	Installs    []binding.PinnedInstall `json:"installs,omitempty"`
	Executables []ExecutableRecord      `json:"executables,omitempty"`
}

// Step is one stage of the pipeline as it actually happened.
type Step struct {
	ID         string `json:"id"`
	Title      string `json:"title,omitempty"`
	Capability string `json:"capability"`
	// Profile and ActionID are what the capability resolved to on this machine.
	Profile  DocumentRef `json:"profile"`
	ActionID string      `json:"action_id"`
	// JobID is the job record with the full logs. The manifest carries the
	// classification; the job carries the bytes.
	JobID string    `json:"job_id,omitempty"`
	State job.State `json:"state"`
	// Command is the argv the executor passed to the operating system.
	Command *job.CommandPreview `json:"command,omitempty"`
	// PreviewMatched records that the command shown before the step ran was the
	// command that ran, compared after substituting the paths that differ
	// because a preview and a run get different job directories. False with an
	// explanation in PreviewDifference is a build that failed.
	PreviewMatched    bool   `json:"preview_matched"`
	PreviewDifference string `json:"preview_difference,omitempty"`

	Options    map[string]string `json:"options,omitempty"`
	Inputs     []FileRecord      `json:"inputs,omitempty"`
	Outputs    []FileRecord      `json:"outputs,omitempty"`
	ExitCode   *int              `json:"exit_code,omitempty"`
	TimedOut   bool              `json:"timed_out,omitempty"`
	Error      string            `json:"error,omitempty"`
	StartedAt  time.Time         `json:"started_at,omitempty"`
	FinishedAt time.Time         `json:"finished_at,omitempty"`
	DurationMS int64             `json:"duration_ms,omitempty"`

	Diagnostics []job.Diagnostic `json:"diagnostics,omitempty"`
	Stdout      job.StreamLog    `json:"stdout"`
	Stderr      job.StreamLog    `json:"stderr"`

	// Skipped marks a step that did not run, with Error saying why. A step
	// after a failure is skipped rather than absent: "it never got there" is
	// information, and an absent step reads as one nobody declared.
	Skipped bool `json:"skipped,omitempty"`
}

// Findings counts what the diagnostic rules classified, by severity.
func (s Step) Findings(severity profile.Severity) int {
	n := 0
	for _, d := range s.Diagnostics {
		if d.Severity == severity {
			n++
		}
	}
	return n
}

// Manifest is the whole record of one build.
type Manifest struct {
	SchemaVersion string `json:"schema_version"`
	BuildID       string `json:"build_id"`
	// Companion is the build of this program that ran it.
	Companion string `json:"companion,omitempty"`
	Platform  string `json:"platform"`
	Label     string `json:"label,omitempty"`

	Pipeline DocumentRef `json:"pipeline"`
	State    job.State   `json:"state"`
	Error    string      `json:"error,omitempty"`
	// Strict says whether an error-severity diagnostic was treated as a failure.
	Strict bool `json:"strict,omitempty"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`

	Inputs  []FileRecord `json:"inputs,omitempty"`
	Tools   []ToolRecord `json:"tools,omitempty"`
	Steps   []Step       `json:"steps"`
	Outputs []FileRecord `json:"outputs,omitempty"`

	// ReproducibleKey is a digest over the *recipe*: the pipeline document, the
	// tools that ran, the inputs, the options and the argv, with the paths and
	// times that differ between two runs left out. Two builds with the same key
	// asked the same tools to do the same thing to the same bytes.
	//
	// It deliberately does not cover the outputs, and that is a measured
	// decision rather than a cautious one. Of ericw-tools 0.18.1: `qbsp` and
	// `vis` are byte-identical run to run, and `light` is not — three runs of
	// one input at four threads produced three different lightmaps, and one
	// thread produced the same one three times. (`auto-pigeon-tools` measured
	// the same thing independently on 20260901, for its own acceptance suite;
	// this build system's digests and the raw tool's agree.) The logs are worse
	// still: every one of them contains the job directory it ran in and how long
	// it took. A key over the outputs would therefore say "not reproducible"
	// about every build ever made, which is a true statement about `light`'s
	// threading and a useless one about the build.
	//
	// What a caller compares instead is the digest of the output it cares
	// about, which is recorded beside every one of them.
	ReproducibleKey string `json:"reproducible_key,omitempty"`
	// Directory is where this build's files are on this machine. Deliberately
	// *not* part of the reproducible key.
	Directory string `json:"directory,omitempty"`
}

// Succeeded reports a build that ran every step and published what it declared.
func (m *Manifest) Succeeded() bool { return m.State == job.Succeeded }

// FailedStep names the step a failed build stopped at, and is empty for a build
// that did not stop at one.
func (m *Manifest) FailedStep() string {
	for _, step := range m.Steps {
		if step.State != job.Succeeded && !step.Skipped {
			return step.ID
		}
	}
	return ""
}

// computeKey fills in ReproducibleKey.
//
// What goes into the recipe key is what a person means by "the same build": the
// pipeline document's digest, each step's profile digest, action and options,
// the argv, the digest of each executable and the digest of each input. What
// stays out is everything that differs between two runs of it — job ids,
// timestamps, durations, absolute paths, exit codes, log sizes — and the
// outputs, for the reason [Manifest.ReproducibleKey] gives.
//
// The argv keeps its *shape*: a machine path becomes a placeholder rather than
// disappearing, so a step that gained an argument still changes the key.
func (m *Manifest) computeKey(roots []string) {
	var b strings.Builder
	write := func(format string, args ...any) { fmt.Fprintf(&b, format, args...); b.WriteByte('\x00') }

	write("schema=%s", SchemaVersion)
	write("pipeline=%s@%s=%s", m.Pipeline.ID, m.Pipeline.Version, m.Pipeline.Digest)
	write("platform=%s", m.Platform)
	for _, input := range m.Inputs {
		write("input=%s:%s:%s", input.Name, input.Role, input.SHA256)
	}
	for _, tool := range m.Tools {
		write("tool=%s@%s=%s:%s", tool.Profile.ID, tool.Profile.Version, tool.Profile.Digest, tool.ToolVersion)
		for _, exe := range tool.Executables {
			write("exe=%s:%s", exe.Name, exe.SHA256)
		}
	}
	for _, step := range m.Steps {
		write("step=%s:%s:%s@%s:%s", step.ID, step.Capability, step.Profile.ID, step.Profile.Version, step.ActionID)
		if step.Skipped {
			write("skipped")
			continue
		}
		for _, name := range sortedKeys(step.Options) {
			write("option=%s=%s", name, step.Options[name])
		}
		if step.Command != nil {
			write("exec=%s", generalize(step.Command.Executable, roots))
			for i, arg := range step.Command.Args {
				write("arg%d=%s", i, generalize(arg, roots))
			}
		}
		for _, out := range step.Outputs {
			// Whether each declared output appeared, not what was in it.
			write("out=%s:%s:%t", out.Name, out.Role, out.Missing)
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	m.ReproducibleKey = "sha256:" + hex.EncodeToString(sum[:])
}

// generalize replaces a machine-and-run-specific prefix with a placeholder, so
// that two builds of the same thing agree and a build that runs something else
// still disagrees.
func generalize(value string, roots []string) string {
	// Longest first: a job directory can sit inside a builds directory, and
	// replacing the shorter one first would leave the rest of the path in.
	ordered := append([]string(nil), roots...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, root := range ordered {
		if root == "" {
			continue
		}
		if value == root {
			return "<run>"
		}
		if strings.HasPrefix(value, root+string(filepath.Separator)) {
			// The tail is kept, with the job id removed: a job id is a
			// timestamp and eight random bytes, and no two runs share one.
			tail := filepath.ToSlash(value[len(root)+1:])
			return "<run>/" + stripJobID(tail)
		}
	}
	return value
}

// stripJobID removes a leading path element that is a job id, which is what
// makes two runs of the same build produce the same argv shape.
func stripJobID(tail string) string {
	first, rest, found := strings.Cut(tail, "/")
	if !found {
		if job.ValidID(first) {
			return "<job>"
		}
		return tail
	}
	if job.ValidID(first) {
		return "<job>/" + rest
	}
	return tail
}

// Save writes the manifest into a build directory.
func (m *Manifest) Save(dir string) error {
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("build: encoding the manifest: %w", err)
	}
	path := filepath.Join(dir, ManifestFileName)
	temporary := path + ".writing"
	if err := os.WriteFile(temporary, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("build: writing %s: %w", path, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("build: publishing %s: %w", path, err)
	}
	return nil
}

// LoadManifest reads one manifest.
func LoadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("build: reading %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("build: %s is not a readable manifest: %w", path, err)
	}
	if m.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("build: %s is %q; this build reads %q", path, m.SchemaVersion, SchemaVersion)
	}
	return &m, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
