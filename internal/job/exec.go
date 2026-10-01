package job

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/failure"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// Supervision: one process, started from an argument array, watched until it
// and everything it started have stopped.
//
// # There is no shell
//
// [exec.Cmd] is given an executable path and a []string. Nothing here builds a
// command line, nothing splits one, and nothing passes one to `sh -c` or
// `cmd /c`. That is the whole of the injection defence and it is structural: an
// argument containing `; rm -rf ~` is one argument, whose value is
// `; rm -rf ~`, because argv is a list of strings all the way down to
// execve. There is no parser between the document and the kernel for a payload
// to be interesting to.
//
// # Stopping is a tree operation
//
// `cmd.Process.Kill` reaches one pid. A compiler with worker processes, or an
// engine started through a wrapper, leaves children that keep the CPU and the
// files. So a stop is: signal the whole process group, wait a grace period for
// it to shut down tidily, and then take it down forcefully. See proc_unix.go
// and proc_windows.go.
//
// # The executor never blocks the program it is watching
//
// Both streams are drained continuously at a fixed buffer size. What is *kept*
// is bounded; what is read never is. See logbuf.go for why that is the only
// safe way round.

// defaultTimeout bounds a job whose action declares no timeout of its own.
//
// An hour. Long enough for a real vis pass on a large map, short enough that a
// tool that has hung does not hold a slot forever. An action that needs more
// says so in the document, which is a number a user can see before approving.
const defaultTimeout = time.Hour

// terminateGrace is how long a process tree gets between the polite signal and
// the forceful one.
const terminateGrace = 5 * time.Second

// reaperGrace is how long the executor waits, after the process it started has
// exited, for anything still holding its output to let go.
//
// A child that outlives its parent keeps the write end of the output pipe open,
// and a reader waiting for end-of-file on that pipe waits forever. Past this
// point the tree is taken down and the pipes are closed from this side, so a
// stray grandchild cannot make a finished job look like a running one.
const reaperGrace = 3 * time.Second

// cancelPollInterval is how often the executor looks for a stop asked for by
// another process. See [Store.RequestCancel].
const cancelPollInterval = 500 * time.Millisecond

// stopReason says why a process was taken down.
type stopReason int

const (
	stopNone stopReason = iota
	stopCancelled
	stopTimedOut
	stopShutdown
)

// execution is one supervised run.
type execution struct {
	id         string
	layout     layout
	invocation profile.Invocation
	action     profile.Action
	store      *Store
	redactor   *Redactor
	// mirror receives output live, for a CLI that is showing a build as it
	// happens. Nil when nobody is watching.
	mirror     io.Writer
	lookupEnv  func(string) (string, bool)
	maxTimeout time.Duration
	// onStarted is called once the process exists, with its pid, so the job
	// record moves to Running before anything is read.
	onStarted func(pid int, command *CommandPreview) error
	// onCaptures hands the two live captures to whoever serves a running job's
	// output (output.go). Nil when nobody does.
	onCaptures func(stdout, stderr *capture)
}

// outcome is what one supervised run produced.
type outcome struct {
	command     *CommandPreview
	exitCode    *int
	stdout      StreamLog
	stderr      StreamLog
	diagnostics []Diagnostic
	reason      stopReason
	// err is why the run did not succeed. Nil means the process exited with one
	// of the action's success codes.
	err error
}

// preview builds the normalized argv record without running anything.
func preview(invocation profile.Invocation, l layout, action profile.Action, lookupEnv func(string) (string, bool), redactor *Redactor) *CommandPreview {
	_, entries := environmentFor(l, invocation.Command.WorkingDir, invocation.Command.Env, action.Environment, lookupEnv)
	args := redactor.RedactAll(invocation.Command.Args)
	parts := make([]string, 0, len(args)+1)
	for _, part := range append([]string{invocation.Command.Executable}, args...) {
		parts = append(parts, shellQuote(part))
	}
	for i := range entries {
		entries[i].Value = redactor.Redact(entries[i].Value)
	}
	return &CommandPreview{
		Executable: invocation.Command.Executable,
		Args:       args,
		WorkingDir: invocation.Command.WorkingDir,
		Env:        entries,
		// The digest is of the real command, not of this redacted view: it is
		// what says two profiles run the same thing, and it has to survive the
		// display layer.
		Digest: invocation.Command.Digest(),
		Shell:  strings.Join(parts, " "),
	}
}

// checkExecutable reports whether the resolved executable can actually be run,
// in a sentence that names what to do about it.
//
// Done before exec rather than relying on its error, because "fork/exec
// /long/path: no such file or directory" does not tell a user whether the tool
// is missing or the profile is wrong, and this is the single most common
// failure on a machine where nothing has been acquired yet.
func checkExecutable(path string) error {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return failure.As(failure.ToolUnavailable,
			fmt.Errorf("job: %s does not exist; acquire the tool, or point the Companion at a copy you already have", path))
	case err != nil:
		return failure.As(failure.ToolUnavailable, fmt.Errorf("job: %s: %w", path, err))
	case info.IsDir():
		return failure.As(failure.ToolUnavailable, fmt.Errorf("job: %s is a directory, not a program", path))
	case !info.Mode().IsRegular():
		return failure.As(failure.ToolUnavailable, fmt.Errorf("job: %s is %s", path, describeMode(info.Mode())))
	case runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0:
		return failure.As(failure.ToolUnavailable,
			fmt.Errorf("job: %s is not executable; check its permissions", path))
	}
	return nil
}

// run starts the process and supervises it to the end.
func (e *execution) run(ctx context.Context) outcome {
	command := preview(e.invocation, e.layout, e.action, e.lookupEnv, e.redactor)
	result := outcome{command: command}

	if err := checkExecutable(e.invocation.Command.Executable); err != nil {
		result.err = err
		return result
	}
	if err := os.MkdirAll(e.invocation.Command.WorkingDir, 0o700); err != nil {
		result.err = fmt.Errorf("job: creating the working directory %s: %w", e.invocation.Command.WorkingDir, err)
		return result
	}

	environment, _ := environmentFor(e.layout, e.invocation.Command.WorkingDir, e.invocation.Command.Env, e.action.Environment, e.lookupEnv)

	// exec.Command, not exec.CommandContext: the context still stops this run,
	// but through signalTree below, so the whole process group goes rather than
	// only the leader.
	process := exec.Command(e.invocation.Command.Executable, e.invocation.Command.Args...)
	process.Dir = e.invocation.Command.WorkingDir
	process.Env = environment
	// Nothing on stdin. A build tool that decides to ask a question gets
	// end-of-file rather than a terminal, so it fails instead of waiting for a
	// person who is not there.
	process.Stdin = nil
	configureProcessGroup(process)

	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		result.err = fmt.Errorf("job: creating the stdout pipe: %w", err)
		return result
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		stdoutRead.Close()
		stdoutWrite.Close()
		result.err = fmt.Errorf("job: creating the stderr pipe: %w", err)
		return result
	}
	process.Stdout = stdoutWrite
	process.Stderr = stderrWrite

	if err := process.Start(); err != nil {
		stdoutRead.Close()
		stdoutWrite.Close()
		stderrRead.Close()
		stderrWrite.Close()
		result.err = failure.As(failure.ToolUnavailable,
			fmt.Errorf("job: starting %s: %w", filepath.Base(e.invocation.Command.Executable), err))
		return result
	}
	// The child has its own descriptors now. Closing these is what makes the
	// readers below see end-of-file when the last writer exits.
	stdoutWrite.Close()
	stderrWrite.Close()

	if e.onStarted != nil {
		if err := e.onStarted(process.Process.Pid, command); err != nil {
			// Recording that it started failed. Stopping is the only honest
			// response: a process nothing recorded is a process nothing will
			// clean up.
			_ = signalTree(process, false)
			_ = process.Wait()
			stdoutRead.Close()
			stderrRead.Close()
			result.err = err
			return result
		}
	}

	diagnostics := &diagnosticCollector{}
	stdout := newCapture("stdout", e.mirror, e.lineHandler(diagnostics))
	stderr := newCapture("stderr", e.mirror, e.lineHandler(diagnostics))
	if e.onCaptures != nil {
		e.onCaptures(stdout, stderr)
	}

	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); stdout.drain(stdoutRead) }()
	go func() { defer readers.Done(); stderr.drain(stderrRead) }()

	// Wait runs in its own goroutine so the supervisor can select on the
	// process finishing. That is only safe because the output streams are
	// plain *os.File pipes this function owns: Wait neither closes them nor
	// waits on a copying goroutine, which it would if these were StdoutPipe.
	waited := make(chan error, 1)
	go func() { waited <- process.Wait() }()

	reason, waitErr := e.supervise(ctx, process, waited)

	// The leader has exited. Anything still holding the output pipes is a child
	// it left behind; give it a moment, then take the tree down and close this
	// side so the readers cannot wait forever.
	drained := make(chan struct{})
	go func() { readers.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(reaperGrace):
		if process.Process != nil && processGroupAlive(process.Process.Pid) {
			// Something the job started is still running and still holding the
			// output pipe. The job is over; its leftovers are not allowed to
			// keep it looking unfinished.
			_ = signalTree(process, false)
		}
		stdoutRead.Close()
		stderrRead.Close()
		<-drained
	}
	stdoutRead.Close()
	stderrRead.Close()

	if err := e.store.WriteLog(e.id, stdoutLogName, stdout.bytesKept()); err != nil {
		result.err = err
	}
	if err := e.store.WriteLog(e.id, stderrLogName, stderr.bytesKept()); err != nil && result.err == nil {
		result.err = err
	}
	result.stdout = stdout.summary(stdoutLogName)
	result.stderr = stderr.summary(stderrLogName)
	result.diagnostics = diagnostics.list()
	result.reason = reason

	code, exited := exitCodeOf(waitErr)
	if exited {
		result.exitCode = &code
	}
	if result.err == nil {
		result.err = e.classify(reason, waitErr, code, exited)
	}
	return result
}

// classify turns a wait result into the job's verdict.
func (e *execution) classify(reason stopReason, waitErr error, code int, exited bool) error {
	switch reason {
	case stopTimedOut:
		return failure.As(failure.TimedOut, fmt.Errorf("job: %s did not finish within %s and was stopped",
			filepath.Base(e.invocation.Command.Executable), e.timeout()))
	case stopCancelled:
		return nil // The service records Cancelled; that is not a failure.
	case stopShutdown:
		return nil
	}
	if !exited {
		if waitErr != nil {
			return fmt.Errorf("job: waiting for %s: %w", filepath.Base(e.invocation.Command.Executable), waitErr)
		}
		return nil
	}
	for _, success := range e.invocation.SuccessExitCodes {
		if code == success {
			return nil
		}
	}
	return failure.As(failure.ToolFailed,
		fmt.Errorf("job: %s exited with status %d", filepath.Base(e.invocation.Command.Executable), code))
}

func exitCodeOf(err error) (int, bool) {
	if err == nil {
		return 0, true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if code := exitErr.ExitCode(); code >= 0 {
			return code, true
		}
		// Killed by a signal: there is no exit status, and reporting one would
		// be inventing the program's answer.
		return 0, false
	}
	return 0, false
}

func (e *execution) timeout() time.Duration {
	if e.invocation.TimeoutSeconds > 0 {
		requested := time.Duration(e.invocation.TimeoutSeconds) * time.Second
		if e.maxTimeout > 0 && requested > e.maxTimeout {
			return e.maxTimeout
		}
		return requested
	}
	if e.maxTimeout > 0 {
		return e.maxTimeout
	}
	return defaultTimeout
}

// supervise watches for the three reasons to stop early and takes the tree down
// when one happens. It returns once the process has exited, with the reason and
// the wait result.
func (e *execution) supervise(ctx context.Context, process *exec.Cmd, waited <-chan error) (stopReason, error) {
	deadline := time.NewTimer(e.timeout())
	defer deadline.Stop()
	poll := time.NewTicker(cancelPollInterval)
	defer poll.Stop()

	// stop signals the tree, gives it a grace period to shut down tidily, then
	// takes it down forcefully, and returns once the leader has been reaped.
	stop := func(reason stopReason) (stopReason, error) {
		_ = signalTree(process, true)
		select {
		case err := <-waited:
			return reason, err
		case <-time.After(terminateGrace):
		}
		_ = signalTree(process, false)
		return reason, <-waited
	}

	for {
		select {
		case err := <-waited:
			return stopNone, err
		case <-ctx.Done():
			// The Companion is shutting down. Stop the tree; the service
			// records the job as interrupted rather than failed, because the
			// reason it stopped was this program, not that one.
			return stop(stopShutdown)
		case <-deadline.C:
			return stop(stopTimedOut)
		case <-poll.C:
			if e.store.CancelRequested(e.id) {
				return stop(stopCancelled)
			}
		}
	}
}

// lineHandler applies the action's diagnostic rules to each line of output.
//
// Rules never suppress: the line is already in the raw log by the time this
// runs, and a rule's only effect is to add a classification. A wrapper that
// could hide a tool's own words would be a wrapper you could not check.
func (e *execution) lineHandler(collector *diagnosticCollector) func(string, int64, []byte) {
	rules := e.invocation.Diagnostics
	if len(rules) == 0 {
		return nil
	}
	return func(stream string, number int64, raw []byte) {
		view := e.redactor.UserView(raw)
		for _, rule := range rules {
			if !rule.Matches(stream, view) {
				continue
			}
			message := rule.Message
			if message == "" {
				message = view
			}
			collector.add(Diagnostic{
				RuleID:   rule.ID,
				Severity: rule.Severity,
				Stream:   stream,
				Line:     int(number),
				Message:  message,
				Hint:     rule.Hint,
				Raw:      view,
				Class:    rule.Class,
				Fatal:    rule.Fatal,
			})
		}
	}
}
