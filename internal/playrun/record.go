// Package playrun is the durable coordinator behind one Build & Run.
//
// # What it is
//
// One press of **Build & Run** is eight things: download the exact map
// revision, download that revision's texture bundle, check the bundle is
// compiler-ready, convert APMap to `.map` through the extractor, compile,
// install the level and its WADs into the owned mod directory, verify what was
// installed, and start the engine. Each of those already has an implementation
// in this program. This package is the ORDER, the RECORD and the CANCELLATION —
// it owns no downloading, no extraction, no compiling and no launching of its
// own, and every one of them arrives as a function in [Deps].
//
// # Why it is durable and not a promise in a browser
//
// The sequence outlives the tab. A compile is minutes; a reload, a crash, a
// closed laptop lid and a second window are all ordinary. So every transition
// is written to disk before it is reported, the record carries the exact
// identities rather than display labels, and a fresh process reading the record
// can say what happened without replaying anything.
//
// # Why the identities are exact
//
// A run records the map asset id and the exact revision, the texture-export
// manifest's digest, the extractor's identity, the build manifest id, every
// staged file with its digest, the engine profile and action, and the exact
// argv. A record that said "dm1, latest" would be a record that means something
// different tomorrow.
package playrun

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/build"
)

// SchemaVersion versions the record.
const SchemaVersion = "aucom.play-run/1.0"

// State is where a run is.
//
// The states are the ones a person asks about, not the ones the code happens to
// have: "it is downloading the textures" is an answer, and "stage 3 of 8" is
// not. A terminal state is [Succeeded], [Failed] or [Cancelled].
type State string

const (
	// Queued: validated and not started.
	Queued State = "queued"
	// DownloadingMap: fetching the exact map revision.
	DownloadingMap State = "downloading_map"
	// DownloadingTextures: fetching and verifying that revision's texture bundle.
	DownloadingTextures State = "downloading_textures"
	// Converting: the extractor is turning APMap into a Quake `.map`, out of
	// process.
	Converting State = "converting"
	// Compiling: the build pipeline is running. [Record.BuildID] and
	// [Record.CurrentStep] say which step and which job.
	Compiling State = "compiling"
	// Installing: the level, its WADs and the build manifest are being staged
	// into the owned mod directory.
	Installing State = "installing"
	// Launching: the engine is being started.
	Launching State = "launching"
	// Running: the engine started. A long-running engine being alive is a
	// SUCCESSFUL LAUNCH, not a request that anything waits for it to exit.
	Running State = "running"
	// Succeeded: the automated sequence completed.
	Succeeded State = "succeeded"
	// Failed: it stopped at the stage [Record.FailedAt] names.
	Failed State = "failed"
	// Cancelled: the user stopped it, and nothing is half-installed.
	Cancelled State = "cancelled"
)

// States is every state, in the order a successful run passes through them.
var States = []State{
	Queued, DownloadingMap, DownloadingTextures, Converting, Compiling,
	Installing, Launching, Running, Succeeded, Failed, Cancelled,
}

// Terminal reports whether a run in this state has stopped.
func (s State) Terminal() bool {
	return s == Succeeded || s == Failed || s == Cancelled
}

// Active reports whether a run in this state is still doing something.
func (s State) Active() bool { return !s.Terminal() }

// Title is the plain-language name a person reads.
func (s State) Title() string {
	switch s {
	case Queued:
		return "Waiting to start"
	case DownloadingMap:
		return "Downloading the map"
	case DownloadingTextures:
		return "Downloading the textures"
	case Converting:
		return "Converting the map"
	case Compiling:
		return "Compiling"
	case Installing:
		return "Installing into the game folder"
	case Launching:
		return "Starting the game"
	case Running:
		return "Running"
	case Succeeded:
		return "Finished"
	case Failed:
		return "Failed"
	case Cancelled:
		return "Cancelled"
	default:
		return string(s)
	}
}

// Request is one Build & Run, in exact identities.
//
// Nothing here is a display label. A run that recorded "dm1" and "EricW tools"
// would be a run nobody could repeat, and a retry that re-resolved a label
// could resolve it to something else.
type Request struct {
	// The map.
	AssetType string `json:"asset_type"`
	AssetID   string `json:"asset_id"`
	// RevisionID names exactly one version for ever.
	RevisionID string `json:"revision_id"`
	// RevisionNumber is the same version's number, which is what AUB's texture
	// export is addressed by. Both are recorded because the two halves of the
	// backend speak different dialects of "which version", and guessing one
	// from the other is how a current export gets paired with a historical map.
	RevisionNumber int `json:"revision_number"`
	// SourceFile names which file of that revision is the map, for a revision
	// that holds more than one. Empty means the revision's single file.
	SourceFile string `json:"source_file,omitempty"`

	// The build.
	PipelineID string                       `json:"pipeline_id"`
	Options    map[string]map[string]string `json:"options,omitempty"`
	Strict     bool                         `json:"strict,omitempty"`

	// The run.
	EngineProfileID string `json:"engine_profile_id"`
	EngineActionID  string `json:"engine_action_id"`
	// GameRoot is the installed game's base directory, as the engine's binding
	// resolved it. Recorded so the record says where files were written even
	// after somebody changes the binding.
	GameRoot string `json:"game_root"`
	// ModName is the directory beside `id1`. Defaults to [DefaultMod].
	ModName string `json:"mod_name"`
	// MapName is the sanitized name the engine is given for `+map`.
	MapName string `json:"map_name"`

	// Label is a short human name for the Jobs list. Optional.
	Label string `json:"label,omitempty"`

	// OwnWADsDir is a folder on this machine the person named in the review,
	// holding their own copy of WADs Auto-Pigeon may not redistribute. Used
	// only for the WADs AUB refused to carry, each by its exact file name, and
	// every one is recorded. Empty means none: a bundle that is not
	// compiler-ready stops the run, as it always did. See ownwads.go.
	OwnWADsDir string `json:"own_wads_dir,omitempty"`

	// Listing, when set, lists a hosted game in Live Games once the engine is
	// running — the listing the person saw previewed in the review. Nil for a
	// game nobody else can join, and for a host who chose not to list it.
	Listing *Listing `json:"listing,omitempty"`
}

// Listing is how a hosted Build & Run appears in Live Games.
type Listing struct {
	// Title is what the game is called in the listing.
	Title string `json:"title"`
	// Visibility is AUB's: public, unlisted or private. AUB, not this
	// program, decides what an address on a home network may be.
	Visibility string `json:"visibility"`
	// EndpointHost and EndpointPort are where players connect, as the person
	// saw and confirmed them in step 3.
	EndpointHost string `json:"endpoint_host"`
	EndpointPort int    `json:"endpoint_port"`
}

// DefaultMod is the game directory a Build & Run installs into.
//
// A sibling of `id1`, never `id1` itself: see internal/engine, which refuses
// every directory a Quake installation already owns.
const DefaultMod = "auto-pigeon"

// Normalize fills in the defaults and refuses a request that cannot be run.
//
// Everything checkable without a network is checked here, before a byte is
// downloaded — which is what "validate the complete plan before downloading or
// starting a compiler" means.
func (r *Request) Normalize() error {
	r.AssetType = strings.TrimSpace(r.AssetType)
	if r.AssetType == "" {
		r.AssetType = "map"
	}
	r.AssetID = strings.TrimSpace(r.AssetID)
	r.RevisionID = strings.TrimSpace(r.RevisionID)
	r.PipelineID = strings.TrimSpace(r.PipelineID)
	r.EngineProfileID = strings.TrimSpace(r.EngineProfileID)
	r.EngineActionID = strings.TrimSpace(r.EngineActionID)
	r.GameRoot = strings.TrimSpace(r.GameRoot)
	r.MapName = strings.TrimSpace(r.MapName)
	r.OwnWADsDir = strings.TrimSpace(r.OwnWADsDir)
	if r.ModName = strings.TrimSpace(r.ModName); r.ModName == "" {
		r.ModName = DefaultMod
	}

	switch {
	case r.AssetID == "":
		return fmt.Errorf("playrun: a run needs a map")
	case r.RevisionID == "":
		return fmt.Errorf("playrun: a run needs the exact revision of that map, never `current`")
	case r.RevisionNumber <= 0:
		return fmt.Errorf("playrun: a run needs that revision's number, which is how the textures are addressed")
	case r.PipelineID == "":
		return fmt.Errorf("playrun: a run needs a build profile")
	case r.EngineProfileID == "":
		return fmt.Errorf("playrun: a run needs an engine profile")
	case r.EngineActionID == "":
		return fmt.Errorf("playrun: a run needs to know which of that engine's actions to start")
	case r.GameRoot == "":
		return fmt.Errorf("playrun: this engine has no game folder set on this machine yet")
	case r.MapName == "":
		return fmt.Errorf("playrun: a run needs a map name; it is what the engine is given for +map")
	case r.Listing != nil && strings.TrimSpace(r.Listing.Title) == "":
		return fmt.Errorf("playrun: a listed game needs a title; it is what Live Games calls it")
	case r.OwnWADsDir != "" && !filepath.IsAbs(r.OwnWADsDir):
		return fmt.Errorf("playrun: the folder with your own WADs must be written as an absolute path")
	}

	return nil
}

// Stage is one state a run passed through, and how long it took.
//
// A message without a duration is an opinion. Every stage records when it
// started and when it finished, so the Activity panel shows elapsed time rather
// than a spinner, and so a failure can be attributed to the stage it happened
// in rather than to the run as a whole.
type Stage struct {
	State      State     `json:"state"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	Error      string    `json:"error,omitempty"`
	// Detail is the one technical sentence behind the plain-language state:
	// which revision, which digest, which job.
	Detail string `json:"detail,omitempty"`
}

// StagedFile is one file a run installed.
type StagedFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// Record is the whole durable record of one Build & Run.
type Record struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`

	Request Request `json:"request"`
	State   State   `json:"state"`
	// FailedAt names the stage a failed run stopped in. Empty unless State is
	// [Failed].
	FailedAt State  `json:"failed_at,omitempty"`
	Error    string `json:"error,omitempty"`

	// Remedy is what the user can do about the failure, in their own terms —
	// "sign in again", "ask the map's owner for the missing WAD". Empty when
	// there is nothing to suggest beyond the error.
	Remedy string `json:"remedy,omitempty"`

	CreatedAt  time.Time `json:"created_at"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`

	Stages []Stage `json:"stages,omitempty"`

	// --- what each stage produced, by identity ---

	// MapFile is the local path the map revision materialized at, and the
	// source record that says which revision it was.
	MapFile   string           `json:"map_file,omitempty"`
	MapSource *build.SourceRef `json:"map_source,omitempty"`

	// Bundle is the verified texture export: its schema, map, revision,
	// digest, ordered declaration and carried files.
	Bundle *build.BundleRef `json:"bundle,omitempty"`
	// BundleRoot is where the verified bundle's content root is on this
	// machine. Diagnostic: the identity above is the portable half.
	BundleRoot string `json:"bundle_root,omitempty"`
	// OwnWADs are the WADs this run took from the person's own folder to
	// complete a bundle AUB could not carry them in, with the digest each was
	// copied at. Empty for an ordinary run.
	OwnWADs []StagedFile `json:"own_wads,omitempty"`

	// Extractor identifies the AUE that ran, and whether it was verified.
	Extractor *ExtractorRef `json:"extractor,omitempty"`
	// ConvertedMap is the `.map` the extractor produced, when one was needed.
	ConvertedMap string `json:"converted_map,omitempty"`

	// BuildID names the build manifest, and CurrentStep and CurrentJob say
	// which step of it is running — which is what a Cancel button needs.
	BuildID     string `json:"build_id,omitempty"`
	CurrentStep string `json:"current_step,omitempty"`
	CurrentJob  string `json:"current_job,omitempty"`

	// Installed is every file staged into the mod directory, with the digest it
	// was staged at. Verified again immediately before launch.
	Installed    []StagedFile `json:"installed,omitempty"`
	InstalledDir string       `json:"installed_dir,omitempty"`

	// Launch is the engine's identity and the exact argv, element by element.
	Launch *LaunchRecord `json:"launch,omitempty"`

	// Warnings are what a stage let through but a person must be told about —
	// textures the compiler did not find, so the level shows a placeholder.
	// A run with warnings still succeeds; the Activity card shows each one.
	Warnings []string `json:"warnings,omitempty"`

	// RetryOf names the attempt this one repeats. A retry is a NEW record
	// linked to the previous one, never a re-run in place: the record of what
	// happened the first time is the thing a retry most often needs.
	RetryOf string `json:"retry_of,omitempty"`
}

// ExtractorRef is which extractor ran.
type ExtractorRef struct {
	Version  string `json:"version"`
	Protocol string `json:"protocol,omitempty"`
	Path     string `json:"path,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	// Verified is internal/aue's one recording of managed-versus-override. It
	// travels here rather than being recomputed, because a caller that derived
	// "was this verified" from four other fields is a caller that will one day
	// derive it wrong.
	Verified bool `json:"verified"`
}

// LaunchRecord is the engine that was started.
type LaunchRecord struct {
	ProfileID string `json:"profile_id"`
	ActionID  string `json:"action_id"`
	JobID     string `json:"job_id,omitempty"`

	// Executable and Args are the argv as the operating system received it:
	// the program, then each argument as its own element. Never a command
	// string — see internal/job, which is the only thing that starts a process
	// here and does not build one.
	Executable string   `json:"executable"`
	Args       []string `json:"args,omitempty"`
	WorkingDir string   `json:"working_dir,omitempty"`
}

// Elapsed is how long a run has been going, or took.
func (r *Record) Elapsed(now time.Time) time.Duration {
	if r.StartedAt.IsZero() {
		return 0
	}
	if !r.FinishedAt.IsZero() {
		return r.FinishedAt.Sub(r.StartedAt)
	}

	return now.Sub(r.StartedAt)
}

// Cancellable reports whether stopping this run now would mean anything.
func (r *Record) Cancellable() bool { return r.State.Active() }

// Retryable reports whether this run can be attempted again.
//
// A succeeded run is not retried — it is run again, which is a new request —
// and a run still going is cancelled first.
func (r *Record) Retryable() bool { return r.State == Failed || r.State == Cancelled }
