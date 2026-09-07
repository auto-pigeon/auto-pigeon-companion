package aue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// EnvBinaryOverride names an on-disk extractor to use instead of the managed
// one.
//
// It exists for development — building the extractor from a checkout and
// driving it from here — and for a support case where somebody must be moved
// onto a patched build before a release. It is LOCAL: nothing produced with it
// is uploaded, and every surface that describes an extractor says the override
// is unverified.
const EnvBinaryOverride = "AUCOM_AUE_BINARY"

// LegacyEnvBinaryOverride is the first Companion bootstrap's spelling, still
// read so an existing development setup keeps working.
const LegacyEnvBinaryOverride = "AUC_AUE_BINARY"

// DefaultTimeout bounds one invocation.
//
// Ten minutes: an extraction over a whole corpus is minutes of real work, and
// an invocation that has been silent for ten is one that is not coming back. It
// bounds the WHOLE run rather than a read, because the failure and the hang
// look the same from here — a process producing one line every nine minutes
// keeps a per-read deadline satisfied for ever.
const DefaultTimeout = 10 * time.Minute

// DefaultGrace is how long a cancelled run has after SIGTERM.
//
// The extractor's published contract says a supervised run ends deliberately on
// SIGTERM and writes a terminal record saying why. Five seconds is long enough
// for that and short enough that a build which ignores the signal does not hold
// a user's Ctrl-C indefinitely.
const DefaultGrace = 5 * time.Second

// maxOutputBytes bounds what one invocation may return.
//
// A subprocess is not a trusted producer of unbounded output: it can be a
// wrong build, a corrupted one, or the right one asked for a corpus report
// nobody expected to be a gigabyte. 64 MiB is far above any real answer and
// finite.
const maxOutputBytes = 64 << 20

// ErrNoExtractor reports that this Companion has no extractor it may run.
var ErrNoExtractor = errors.New("aue: no verified extractor is installed and " + EnvBinaryOverride + " is not set")

// ErrOutputNotJSON reports a subcommand that was asked for JSON and produced
// something else.
var ErrOutputNotJSON = errors.New("aue: the extractor's output is not the single JSON document this call expects")

// ErrOutputTooLarge reports output over the cap.
var ErrOutputTooLarge = errors.New("aue: the extractor produced more output than this Companion will read")

// Runner invokes one extractor subcommand and returns its stdout.
//
// Implementations must be safe for concurrent use: the local HTTP server
// handles requests on many goroutines.
type Runner interface {
	Run(ctx context.Context, subcommand string, args ...string) ([]byte, error)
	// Provenance says where this runner's executable came from and whether
	// anything verified it. On the interface rather than on the concrete type
	// because every surface that shows an extractor has to show it, and a
	// caller holding a Runner must not have to type-assert to find out whether
	// it is about to run something unverified.
	Provenance() Provenance
}

// ExitError describes an invocation that ran and failed.
//
// The extractor's exit codes are meaningful and its own `protocol` document
// publishes the table: 1 is the operation failing, which is usually the input,
// and 2 is the invocation being wrong, which is a bug in whatever composed the
// argument vector. Collapsing them loses the one distinction that decides who
// needs to be told.
type ExitError struct {
	Subcommand string
	ExitCode   int
	Stderr     string
}

func (err *ExitError) Error() string {
	message := fmt.Sprintf("auto-pigeon-extractor %s failed with exit code %d", err.Subcommand, err.ExitCode)
	if err.Stderr != "" {
		message += ": " + err.Stderr
	}

	return message
}

// TimeoutError describes an invocation this Companion stopped.
type TimeoutError struct {
	Subcommand string
	After      time.Duration
}

func (err *TimeoutError) Error() string {
	return fmt.Sprintf("auto-pigeon-extractor %s did not finish within %s and was stopped",
		err.Subcommand, err.After)
}

// ProcessRunner execs one extractor executable.
//
// The zero value is not usable; a Runner comes from [Resolver.Resolve] or from
// [NewOverrideRunner], and those are the only two ways there are.
type ProcessRunner struct {
	path       string
	provenance Provenance

	// Timeout bounds one invocation. Zero means DefaultTimeout.
	Timeout time.Duration

	// WorkRoot is where per-invocation job directories are created. Empty means
	// the OS temp directory.
	WorkRoot string

	// Grace is how long a cancelled run has after SIGTERM before the runtime
	// stops waiting for it. Zero means DefaultGrace.
	//
	// A field because the right value is a property of what is being run: a
	// real extraction wants seconds to finish writing its terminal record, and
	// a test wants milliseconds. The DEFAULT is the production one, so a caller
	// that never thinks about it gets the patient answer.
	Grace time.Duration

	// Environment is what the child process is given. Nil means this process's
	// own environment plus the deadline variable.
	//
	// NOT scrubbed by default, deliberately: the extractor's own configuration
	// is environment-driven — where its APMap contract is, where the mapper
	// root is — and a runner that cleared the environment would break every
	// deployment that configured one. What is ADDED is the deadline, so the
	// process can end itself with a named reason instead of being killed with
	// none.
	Environment []string

	once sync.Once
}

// Provenance says where this runner's executable came from.
func (r *ProcessRunner) Provenance() Provenance { return r.provenance }

// Path is the executable this runner execs.
func (r *ProcessRunner) Path() string { return r.path }

func (r *ProcessRunner) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}

	return DefaultTimeout
}

func (r *ProcessRunner) grace() time.Duration {
	if r.Grace > 0 {
		return r.Grace
	}

	return DefaultGrace
}

// Run executes one subcommand and returns its stdout.
//
// Four things happen around the exec, and each is a requirement rather than a
// nicety:
//
//   - **a timeout**, bounding the whole run;
//   - **cancellation that the extractor can honour** — SIGTERM first, because
//     its published contract says a supervised run ends deliberately on one and
//     writes a terminal record saying why. SIGKILL is what happens if it does
//     not, after a grace period, and it is the answer that loses the reason;
//   - **an isolated working directory**, fresh per invocation and removed
//     after, so two concurrent invocations cannot see each other's scratch and
//     a relative path in an argument cannot reach this program's own directory;
//   - **a bounded read**, because a subprocess is not a trusted producer of
//     unbounded output.
func (r *ProcessRunner) Run(ctx context.Context, subcommand string, args ...string) ([]byte, error) {
	timeout := r.timeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	jobDir, err := os.MkdirTemp(r.WorkRoot, "aucom-aue-job-")
	if err != nil {
		return nil, fmt.Errorf("aue: creating a job directory: %w", err)
	}
	defer os.RemoveAll(jobDir)

	command := exec.CommandContext(ctx, r.path, append([]string{subcommand}, args...)...)
	command.Dir = jobDir
	command.Env = r.environment(timeout)

	// The extractor's published cancellation contract, honoured rather than
	// assumed: SIGTERM, then a grace period, then whatever the runtime does to
	// a process that ignored it.
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = r.grace()

	var stdout, stderr bytes.Buffer
	capped := &limitedWriter{writer: &stdout, remaining: maxOutputBytes}
	command.Stdout = capped
	command.Stderr = &limitedWriter{writer: &stderr, remaining: 1 << 20}

	runErr := command.Run()

	// Checked BEFORE the exit code, because refusing to read any more closes
	// the pipe and the child then dies of SIGPIPE. Reporting that as "exit 141"
	// would name the symptom and hide the cause.
	if capped.tripped {
		return nil, fmt.Errorf("%w: %s", ErrOutputTooLarge, subcommand)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return stdout.Bytes(), &TimeoutError{Subcommand: subcommand, After: timeout}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return stdout.Bytes(), ctx.Err()
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return stdout.Bytes(), &ExitError{
				Subcommand: subcommand,
				ExitCode:   exitErr.ExitCode(),
				Stderr:     trimmed(stderr.String()),
			}
		}
		return nil, fmt.Errorf("aue: running auto-pigeon-extractor %s: %w", subcommand, runErr)
	}

	return stdout.Bytes(), nil
}

// RunJSON runs a subcommand and decodes its stdout as one JSON document.
//
// The validation is the point, and it is why callers do not simply
// `json.Unmarshal(runner.Run(...))`. A subprocess can exit 0 having printed a
// warning, half a document, or a document with something appended; each of
// those decodes into a partially-filled struct that a caller then acts on.
// Refusing trailing content and an empty body turns three silent
// misinterpretations into one error naming the subcommand.
func (r *ProcessRunner) RunJSON(ctx context.Context, target any, subcommand string, args ...string) error {
	stdout, err := r.Run(ctx, subcommand, args...)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(stdout)) == 0 {
		return fmt.Errorf("%w: %s produced no output", ErrOutputNotJSON, subcommand)
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrOutputNotJSON, subcommand, err)
	}
	if decoder.More() {
		return fmt.Errorf("%w: %s wrote more than one document", ErrOutputNotJSON, subcommand)
	}

	return nil
}

func (r *ProcessRunner) environment(timeout time.Duration) []string {
	base := r.Environment
	if base == nil {
		base = os.Environ()
	}
	// The extractor's own deadline variable, so it can end itself with a named
	// reason before this Companion has to signal it. Its published protocol
	// document names this variable; it is not guessed at here.
	seconds := int64(timeout / time.Second)

	return append(append([]string{}, base...),
		"AUTO_PIGEON_JOB_DEADLINE_SECONDS="+strconv.FormatInt(seconds, 10))
}

// NewOverrideRunner returns a runner for an on-disk executable the user named.
//
// It is a separate constructor rather than a mode of the managed one, so that
// nothing can arrive at an unverified executable by falling through a managed
// path that failed. Every caller of this function is a caller that read the
// environment variable and decided, and the provenance it produces says so.
func NewOverrideRunner(path string) *ProcessRunner {
	return &ProcessRunner{
		path: path,
		provenance: Provenance{
			Mode: ModeDeveloperOverride, Verified: false, Path: path,
			Note: UnverifiedNote,
		},
	}
}

// OverridePath returns the configured developer override, if any.
func OverridePath() string {
	if path := os.Getenv(EnvBinaryOverride); path != "" {
		return path
	}

	return os.Getenv(LegacyEnvBinaryOverride)
}

// Available reports whether a runner can run an extractor at all.
//
// A free function rather than a method because availability is interesting to a
// caller holding any Runner — including a nil one, which is what an
// unconfigured build produces. A runner that knows how to answer cheaply says
// so through the optional interface; one that does not is reported available,
// because it exists and can therefore be tried.
func Available(runner Runner) bool {
	if runner == nil {
		return false
	}
	if checker, ok := runner.(interface{ Available() bool }); ok {
		return checker.Available()
	}

	return true
}

// errOutputTooLarge is what limitedWriter returns past its cap.
var errOutputTooLarge = errors.New("aue: output cap reached")

// limitedWriter refuses past a byte cap instead of growing without bound.
type limitedWriter struct {
	writer    io.Writer
	remaining int64
	// tripped records that the cap was reached, so the caller can report the
	// cause rather than whatever the child died of afterwards.
	tripped bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		if w.remaining > 0 {
			_, _ = w.writer.Write(p[:w.remaining])
			w.remaining = 0
		}
		w.tripped = true

		return 0, errOutputTooLarge
	}
	w.remaining -= int64(len(p))

	return w.writer.Write(p)
}

func trimmed(text string) string { return string(bytes.TrimSpace([]byte(text))) }
