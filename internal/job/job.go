package job

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// SchemaVersion versions the stored job record.
//
// Local state, like a binding: it only has to be readable by the next build of
// this program, not by anybody else's. A record this build does not fully
// understand is refused rather than half-read, because a job record is what
// says whether something ran.
const SchemaVersion = "aucom.job/1.2"

// SupportedSchemaVersions is every job record format this build reads, oldest
// first.
//
// 1.1 added `installs`: which downloaded packages a job ran. The Companion has
// downloaded no program since 2026-09-23, so nothing writes it; it is still
// read, because the store is decoded strictly and an old record must load.
// 1.2 added `session_role`: whether the job is a client, a listen server or a
// dedicated server. A record without it is a record from before engine
// profiles distinguished them, and reading it as "unknown" is true — but a
// running server that a job list showed as an ordinary process is exactly the
// thing the field exists to stop, so the version is named rather than inferred.
var SupportedSchemaVersions = []string{"aucom.job/1.0", "aucom.job/1.1", "aucom.job/1.2"}

// SchemaSupported reports whether this build reads a job record format.
func SchemaSupported(version string) bool {
	for _, v := range SupportedSchemaVersions {
		if v == version {
			return true
		}
	}
	return false
}

// Request is a submission: which action of which profile, with which inputs.
//
// It carries no paths of its own beyond the ones a user chose, and no
// credentials. Everything else — where the tool is, where the roots are — comes
// from the local binding, which is machine state and is not part of the
// request.
type Request struct {
	ProfileID string `json:"profile_id"`
	ActionID  string `json:"action_id"`
	// Inputs maps a declared input name to a file on this machine.
	Inputs map[string]string `json:"inputs,omitempty"`
	// Options and Runtime are the user's choices, checked against the action's
	// declarations by the resolver.
	Options map[string]string `json:"options,omitempty"`
	Runtime map[string]string `json:"runtime,omitempty"`
	// Executables and Roots override the local binding for this one job. They
	// are how a user points the Companion at a copy of a tool they already have
	// before any acquisition mechanism exists.
	Executables map[string]string `json:"executables,omitempty"`
	Roots       map[string]string `json:"roots,omitempty"`
	// Label is a short human name for the job list. Optional.
	Label string `json:"label,omitempty"`
	// RetryOf names the job this one repeats. Set by Retry, never by a caller:
	// an interrupted job is never re-run in place, it is copied into a new one
	// so the record of what happened the first time survives.
	RetryOf string `json:"retry_of,omitempty"`
	// CorrelationID is the cross-stack correlation id the submitter carried
	// (header X-Auto-Pigeon-Correlation-Id), if any. NOT persisted — `json:"-"`
	// — so the record format is unchanged: it lives in the running service
	// only, long enough for a failure report to carry it.
	CorrelationID string `json:"-"`
}

// EnvSource says where one environment variable's value came from.
type EnvSource string

const (
	// EnvFromDocument: the profile's `environment.set`, rendered.
	EnvFromDocument EnvSource = "document"
	// EnvFromHost: named in `environment.inherit` and passed through. The
	// value is deliberately not recorded — see [EnvEntry].
	EnvFromHost EnvSource = "host"
	// EnvFromExecutor: set by this package, the same way for every job.
	EnvFromExecutor EnvSource = "executor"
)

// EnvEntry is one environment variable in a command preview.
//
// A host-inherited variable records its name and not its value. The profile
// validator already refuses to inherit anything that reads like a credential,
// but "already refused the obvious ones" is not a reason to write the rest into
// a file that a user will later attach to a bug report.
type EnvEntry struct {
	Name   string    `json:"name"`
	Value  string    `json:"value,omitempty"`
	Source EnvSource `json:"source"`
}

// CommandPreview is the normalized argv a job will run, or ran.
//
// It is the same value the executor passes to the operating system, not a
// re-rendering of it: [Preview] and [Service.Submit] resolve through exactly
// one function, so a preview a user approved cannot differ from what starts.
// Display is the only thing the quoted form is for; nothing re-executes it.
type CommandPreview struct {
	Executable string     `json:"executable"`
	Args       []string   `json:"args"`
	WorkingDir string     `json:"working_dir"`
	Env        []EnvEntry `json:"env,omitempty"`
	// Digest identifies the command by what it does, from profile.Command.
	Digest string `json:"digest"`
	// Shell is the approximate shell rendering, for display only.
	Shell string `json:"shell"`
}

// Artifact is one collected output.
type Artifact struct {
	// Name is the declared output name; Role is its artifact role.
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
	// Path is where the file was published, outside the workspace, so it
	// survives the workspace being cleaned up.
	Path     string `json:"path,omitempty"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256,omitempty"`
	Optional bool   `json:"optional,omitempty"`
	// Missing records an output the action declared and did not produce. An
	// optional one is a fact; a required one is why the job failed.
	Missing bool `json:"missing,omitempty"`
}

// Diagnostic is one line of output a rule recognised.
type Diagnostic struct {
	RuleID   string           `json:"rule_id"`
	Severity profile.Severity `json:"severity"`
	Stream   string           `json:"stream"`
	Line     int              `json:"line"`
	// Message is the rule's own sentence, or the matched line when it has none.
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	// Raw is the matched line as the user view renders it: valid UTF-8, control
	// characters removed, secrets redacted.
	Raw string `json:"raw"`
}

// StreamLog is what was captured from one output stream.
type StreamLog struct {
	// Bytes is how much the program wrote; Stored is how much was kept.
	Bytes  int64 `json:"bytes"`
	Stored int64 `json:"stored"`
	// Dropped is the middle of a flood: bytes read, counted, and not kept.
	Dropped int64 `json:"dropped"`
	Lines   int64 `json:"lines"`
	// Truncated is Dropped > 0, recorded as its own member so a reader does
	// not have to know that.
	Truncated bool `json:"truncated"`
	// File is the raw log's name inside the job directory. Raw means the bytes
	// the program wrote, invalid UTF-8 and all.
	File string `json:"file,omitempty"`
}

// Owner identifies the process that is supervising a job.
//
// A run identifier, not just a pid: pids are reused, and a stored job claiming
// to be owned by a pid that now belongs to something else is exactly the case
// recovery has to get right.
type Owner struct {
	PID int    `json:"pid,omitempty"`
	Run string `json:"run,omitempty"`
}

// ProcessIdentity is a started program as the operating system knows it: its
// pid, which is also its process group, and when the kernel started it. A pid
// alone is not an identity — it is reused — so a zero StartTicks means "cannot
// be verified here" and recovery then leaves the process alone.
type ProcessIdentity struct {
	PID        int    `json:"pid,omitempty"`
	StartTicks uint64 `json:"start_ticks,omitempty"`
}

// Job is the whole record of one supervised process.
type Job struct {
	SchemaVersion string  `json:"schema_version"`
	ID            string  `json:"id"`
	State         State   `json:"state"`
	Request       Request `json:"request"`

	// What was resolved. Recorded on the job rather than looked up later,
	// because the document on disk can change and the question this answers is
	// "what did this job run", not "what would it run now".
	ProfileID      string        `json:"profile_id,omitempty"`
	ProfileVersion string        `json:"profile_version,omitempty"`
	ProfileDigest  string        `json:"profile_digest,omitempty"`
	ProfileName    string        `json:"profile_name,omitempty"`
	ActionID       string        `json:"action_id,omitempty"`
	ActionTitle    string        `json:"action_title,omitempty"`
	Trust          profile.Trust `json:"trust,omitempty"`
	// SessionRole says what kind of thing this job is: a client, a game other
	// people can join, or a server with nobody at the keyboard.
	//
	// Recorded on the job because a list of running processes that did not
	// distinguish them would be a list in which a user cannot tell whether
	// their machine is currently reachable from the internet. Empty on a tool
	// job, which is not a session at all.
	SessionRole profile.SessionRole `json:"session_role,omitempty"`

	// Installs is LEGACY: the managed downloads a job recorded before
	// 2026-09-23, by cache digest. Read, because the store is decoded strictly
	// and an old record must keep loading; never written.
	Installs []string `json:"installs,omitempty"`

	Command   *CommandPreview `json:"command,omitempty"`
	Workspace string          `json:"workspace,omitempty"`
	// ArtifactDir is where collected outputs were published.
	ArtifactDir string     `json:"artifact_dir,omitempty"`
	Artifacts   []Artifact `json:"artifacts,omitempty"`

	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	Stdout      StreamLog    `json:"stdout"`
	Stderr      StreamLog    `json:"stderr"`

	// ExitCode is nil until a process has exited. A job that failed before one
	// started has no exit code, and reporting 0 or -1 for that would be a
	// number that looks like the program's answer.
	ExitCode *int `json:"exit_code,omitempty"`
	// Error is why a job failed, in a sentence. Empty on success.
	Error string `json:"error,omitempty"`
	// TimedOut and TimeoutSeconds record the bound and whether it was hit.
	TimedOut       bool `json:"timed_out,omitempty"`
	TimeoutSeconds int  `json:"timeout_seconds,omitempty"`

	CreatedAt  time.Time `json:"created_at"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`

	Owner Owner `json:"owner,omitempty"`
	// Process names the program this job started, precisely enough that
	// recovery can tell the tree a crashed Companion left behind from an
	// unrelated process that has since been given the same pid.
	Process ProcessIdentity `json:"process,omitempty"`
	// History is every state this job has been in, with when and why.
	History []Event `json:"history,omitempty"`
}

// Event is one entry in a job's history.
type Event struct {
	State State     `json:"state"`
	At    time.Time `json:"at"`
	Note  string    `json:"note,omitempty"`
}

// Hosting reports a job that is serving other people: a listen server or a
// dedicated one. It is the question a user actually asks — "is my machine
// hosting a game right now" — and answering it from one place means no caller
// has to remember which of the three roles counts.
func (j *Job) Hosting() bool {
	return j.SessionRole == profile.SessionListen || j.SessionRole == profile.SessionDedicated
}

// Duration is how long the job took, or has taken so far.
func (j *Job) Duration() time.Duration {
	start := j.StartedAt
	if start.IsZero() {
		start = j.CreatedAt
	}
	end := j.FinishedAt
	if end.IsZero() {
		end = time.Now().UTC()
	}
	if end.Before(start) {
		return 0
	}
	return end.Sub(start)
}

// Succeeded reports the plain answer a script wants.
func (j *Job) Succeeded() bool { return j.State == Succeeded }

// Retryable reports whether Retry will accept this job. A job that is still the
// executor's responsibility is not: stopping it is Cancel's business.
func (j *Job) Retryable() bool { return j.State.Terminal() }

// Clone returns a deep copy. Every read path outside this package gets one, so
// a caller holding a job cannot mutate the store's own value.
func (j *Job) Clone() *Job {
	if j == nil {
		return nil
	}
	out := *j
	out.Request = j.Request.clone()
	if j.Command != nil {
		command := *j.Command
		command.Args = append([]string(nil), j.Command.Args...)
		command.Env = append([]EnvEntry(nil), j.Command.Env...)
		out.Command = &command
	}
	out.Installs = append([]string(nil), j.Installs...)
	out.Artifacts = append([]Artifact(nil), j.Artifacts...)
	out.Diagnostics = append([]Diagnostic(nil), j.Diagnostics...)
	out.History = append([]Event(nil), j.History...)
	if j.ExitCode != nil {
		code := *j.ExitCode
		out.ExitCode = &code
	}
	return &out
}

func (r Request) clone() Request {
	out := r
	out.Inputs = copyMap(r.Inputs)
	out.Options = copyMap(r.Options)
	out.Runtime = copyMap(r.Runtime)
	out.Executables = copyMap(r.Executables)
	out.Roots = copyMap(r.Roots)
	return out
}

func copyMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// idPattern is what a job id may look like, and it is checked on every path
// that turns an id into a filename. A job id reaches the store as a path
// element and reaches the HTTP API as a URL segment; anything that is not this
// shape is refused before either.
var idPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{12}$`)

// ValidID reports whether an id is one this package would have minted.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// NewID mints a job id: a UTC timestamp so a directory listing sorts by age,
// and six random bytes so two jobs submitted in the same second cannot collide.
func NewID(now time.Time) (string, error) {
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("job: minting an id: %w", err)
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix[:]), nil
}

// summarise is the one-line rendering the CLI's list uses.
func (j *Job) summarise() string {
	label := j.Request.Label
	if label == "" {
		label = strings.TrimSpace(j.ProfileID + " " + j.ActionID)
	}
	if label == "" {
		label = j.Request.ProfileID + " " + j.Request.ActionID
	}
	return label
}
