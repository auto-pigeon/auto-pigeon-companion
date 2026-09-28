package job

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/fsshare"
)

// Live output: what a program has printed so far, read a bounded chunk at a
// time while it is still running (NEW_265, which subsumed NEW_256).
//
// # Why this exists
//
// A job's two streams are captured in memory and written to disk when the
// process exits (exec.go). That is right for the record and wrong for a person
// watching a four-minute vis pass: the Jobs page polled every second and said
// "(nothing was printed)" the whole time. On Windows it said so even when the
// compiler was busy printing, because a C program's stdout is fully buffered
// when it is a pipe rather than a console — the lines arrive at exit, in one
// block. EricW's tools also write every line to a transcript beside their
// outputs (`level.log`, `vis.log`, `light.log`), flushed as they go, and the
// profile already declares those files. So the view reads whichever of the
// three a program is actually filling.
//
// # What it is not
//
// A polling repair, not a logging framework: no stream, no second transport,
// no second copy on disk. Stdout and stderr are read out of the bounded capture
// the executor already keeps; a transcript is read out of the file the tool
// writes, through the job's own directory. Everything returned is the user
// view — valid UTF-8, no control sequences, credentials redacted — and
// nothing is returned as markup.

// Output source kinds.
const (
	OutputStdout  = "stdout"
	OutputStderr  = "stderr"
	OutputSidecar = "sidecar"
)

// sidecarPrefix names a transcript source: `log:<declared output name>`.
const sidecarPrefix = "log:"

// DefaultOutputChunk and MaxOutputChunk bound one read. A reader that is far
// behind catches up over several polls rather than in one response that holds
// megabytes; the page draws each chunk and asks for the next.
const (
	DefaultOutputChunk = 64 << 10
	MaxOutputChunk     = 256 << 10
)

// OutputRequest is one read.
type OutputRequest struct {
	// Source is `auto`, `stdout`, `stderr` or `log:<name>`. Empty means auto.
	Source string
	// From is the source the reader has been following. With Source auto, an
	// answer from a different source restarts at zero and says so (Reset);
	// with the same one, Offset continues it.
	From string
	// Offset is the cursor a previous answer returned as Next. Negative means
	// "the last Max bytes": a reader opening a long job starts at its end.
	Offset int64
	// Max bounds the returned bytes. Zero means DefaultOutputChunk.
	Max int
}

// OutputSource describes one place a job's output can be read from.
type OutputSource struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Bytes is how much there is to read right now.
	Bytes int64 `json:"bytes"`
	// Exists is false for a declared transcript the tool has not created yet.
	Exists bool `json:"exists"`
}

// OutputChunk is one answer.
type OutputChunk struct {
	Job      string         `json:"job"`
	State    State          `json:"state"`
	Terminal bool           `json:"terminal"`
	Source   OutputSource   `json:"source"`
	Sources  []OutputSource `json:"sources"`
	// Offset is where Text starts in the source; Next is the cursor to send
	// back. Size is how much the source holds now.
	Offset int64 `json:"offset"`
	Next   int64 `json:"next"`
	Size   int64 `json:"size"`
	// Gap is how many bytes were skipped to reach Offset: the dropped middle of
	// a flood, or the start of a long file a reader opened at its end.
	Gap int64 `json:"gap,omitempty"`
	// Reset says the reader's view no longer continues: the source changed, or
	// the file shrank under it (rewritten or rotated). Start again from Text.
	Reset bool   `json:"reset,omitempty"`
	Text  string `json:"text"`
	// Complete is true once the job has stopped and everything has been read:
	// polling this source again cannot return anything new.
	Complete bool `json:"complete"`
}

// liveCaptures is a running job's two streams, while it runs.
type liveCaptures struct {
	stdout, stderr *capture
}

// Output reads the next chunk of a job's output.
func (s *Service) Output(id string, request OutputRequest) (*OutputChunk, error) {
	j, err := s.store.Load(id)
	if err != nil {
		return nil, err
	}
	max := request.Max
	if max <= 0 {
		max = DefaultOutputChunk
	}
	if max > MaxOutputChunk {
		max = MaxOutputChunk
	}

	s.mu.Lock()
	live := s.live[id]
	s.mu.Unlock()
	// A job this process no longer holds captures for, and that is not over, is
	// in the moment between the process exiting and the record being written,
	// or is supervised by another process. Either way its streams are read from
	// disk, which is what exists.
	terminal := j.State.Terminal()

	sources := s.outputSources(j, live)
	chosen, found := chooseSource(sources, request.Source)
	if !found {
		return nil, fmt.Errorf("job: %q is not an output of %s; it has: %s", request.Source, id, sourceIDs(sources))
	}

	offset := request.Offset
	reset := false
	if request.From != "" && request.From != chosen.ID {
		offset, reset = 0, true
	}

	chunk := &OutputChunk{Job: id, State: j.State, Terminal: terminal, Source: chosen, Sources: sources}
	var raw []byte
	switch chosen.Kind {
	case OutputStdout, OutputStderr:
		raw, err = s.readStream(j, live, chosen.Kind, offset, max, chunk)
	default:
		raw, err = s.readSidecar(j, chosen, offset, max, chunk)
	}
	if err != nil {
		return nil, err
	}
	chunk.Reset = chunk.Reset || reset

	// While the program is still writing, a chunk that stops mid-line keeps the
	// part line back for the next read: a credential is never split across two
	// redactions, a UTF-8 sequence is never cut in half, and a line is never
	// drawn twice as it grows. A line longer than the whole chunk is returned
	// as it is, or it would never be returned at all. Once the job is over the
	// last partial line is the final flush and is returned whole.
	if !terminal && len(raw) > 0 && raw[len(raw)-1] != '\n' {
		if cut := bytes.LastIndexByte(raw, '\n'); cut >= 0 {
			raw = raw[:cut+1]
		} else if len(raw) < max {
			raw = raw[:0]
		}
	}
	chunk.Next = chunk.Offset + int64(len(raw))
	chunk.Text = s.redactor().UserView(raw)
	chunk.Complete = terminal && chunk.Next >= chunk.Size
	return chunk, nil
}

// outputSources lists where a job's output can be read, transcripts first.
func (s *Service) outputSources(j *Job, live *liveCaptures) []OutputSource {
	out := make([]OutputSource, 0, len(j.SidecarLogs)+2)
	for _, sidecar := range j.SidecarLogs {
		source := OutputSource{
			ID:    sidecarPrefix + sidecar.Name,
			Kind:  OutputSidecar,
			Label: sidecarLabel(sidecar),
		}
		if path, err := s.sidecarPath(j, sidecar); err == nil {
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				source.Exists, source.Bytes = true, info.Size()
			}
		}
		out = append(out, source)
	}
	for _, stream := range []string{OutputStdout, OutputStderr} {
		source := OutputSource{ID: stream, Kind: stream, Label: streamLabel(stream), Exists: true}
		if live != nil {
			c := live.stdout
			if stream == OutputStderr {
				c = live.stderr
			}
			_, _, total, _ := c.readFrom(0, 0)
			source.Bytes = total
		} else if stream == OutputStdout {
			source.Bytes = j.Stdout.Bytes
		} else {
			source.Bytes = j.Stderr.Bytes
		}
		out = append(out, source)
	}
	return out
}

// chooseSource is the answer to "auto": the transcript the tool is filling,
// when it declared one and has started it; otherwise whichever stream has
// something in it; otherwise the transcript that is still to come, so the
// page can say it is waiting for that file rather than that nothing happened.
func chooseSource(sources []OutputSource, requested string) (OutputSource, bool) {
	if requested != "" && requested != "auto" {
		for _, source := range sources {
			if source.ID == requested {
				return source, true
			}
		}
		return OutputSource{}, false
	}
	for _, source := range sources {
		if source.Kind == OutputSidecar && source.Exists && source.Bytes > 0 {
			return source, true
		}
	}
	for _, kind := range []string{OutputStdout, OutputStderr} {
		for _, source := range sources {
			if source.Kind == kind && source.Bytes > 0 {
				return source, true
			}
		}
	}
	for _, source := range sources {
		if source.Kind == OutputSidecar {
			return source, true
		}
	}
	for _, source := range sources {
		if source.Kind == OutputStdout {
			return source, true
		}
	}
	return OutputSource{}, false
}

func sourceIDs(sources []OutputSource) string {
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, source.ID)
	}
	return strings.Join(ids, ", ")
}

func sidecarLabel(sidecar SidecarLog) string {
	title := sidecar.Title
	if title == "" {
		title = "Log"
	}
	return fmt.Sprintf("%s (%s)", title, filepath.Base(sidecar.Path))
}

func streamLabel(stream string) string {
	if stream == OutputStderr {
		return "Error output (stderr)"
	}
	return "Program output (stdout)"
}

// readStream reads stdout or stderr: out of the live capture while the program
// runs, out of the stored log once it has stopped.
func (s *Service) readStream(j *Job, live *liveCaptures, stream string, offset int64, max int, chunk *OutputChunk) ([]byte, error) {
	if live != nil {
		c := live.stdout
		if stream == OutputStderr {
			c = live.stderr
		}
		if offset < 0 {
			_, _, total, _ := c.readFrom(0, 0)
			offset = max64(0, total-int64(max))
		}
		data, start, total, gap := c.readFrom(offset, max)
		chunk.Offset, chunk.Size, chunk.Gap = start, total, gap
		if offset > total {
			chunk.Reset = true
		}
		return data, nil
	}

	summary := j.Stdout
	name := stdoutLogName
	if stream == OutputStderr {
		summary, name = j.Stderr, stderrLogName
	}
	stored, err := s.store.ReadLog(j.ID, name)
	if err != nil {
		return nil, err
	}
	return storedStreamRead(stored, summary, offset, max, chunk), nil
}

// storedStreamRead maps a stream offset onto the stored log, whose middle may
// have been replaced by the elision marker. The stored file is head, marker,
// tail; the offsets a live reader was given count every byte the program
// wrote, so the same cursor continues across the moment a job ends.
func storedStreamRead(stored []byte, summary StreamLog, offset int64, max int, chunk *OutputChunk) []byte {
	total := summary.Bytes
	if total == 0 && summary.Stored == 0 {
		// A record from before the summary, or a stream nobody wrote to: the
		// file is the stream.
		total = int64(len(stored))
	}
	head, tail := int64(len(stored)), int64(0)
	marker := int64(0)
	if summary.Dropped > 0 {
		head = min64(total, headBytes)
		tail = summary.Stored - head
		marker = int64(len(stored)) - head - tail
		if marker < 0 || tail < 0 {
			// Not the layout this build writes. Read it as a plain file rather
			// than guess at where its parts are.
			head, tail, marker, total = int64(len(stored)), 0, 0, int64(len(stored))
		}
	}
	if offset < 0 {
		offset = max64(0, total-int64(max))
	}
	if offset > total {
		chunk.Reset = true
		offset = 0
	}
	chunk.Size = total
	if offset < head {
		end := min64(head, offset+int64(max))
		chunk.Offset = offset
		return stored[offset:end]
	}
	tailStart := total - tail
	if offset < tailStart {
		chunk.Gap = tailStart - offset
		offset = tailStart
	}
	chunk.Offset = offset
	from := head + marker + (offset - tailStart)
	end := min64(int64(len(stored)), from+int64(max))
	if from > end {
		return nil
	}
	return stored[from:end]
}

// sidecarPath is where a declared transcript is now: in the workspace while
// the job runs (and after it fails, when the workspace is kept), and as the
// published artifact once the job has finished and collected it.
//
// Both answers are checked to be inside the job's own directory, and a
// symbolic link is refused rather than followed: the path came from a profile
// document, and a tool could have replaced the file with a link to somewhere
// else before the view reads it.
func (s *Service) sidecarPath(j *Job, sidecar SidecarLog) (string, error) {
	l := s.store.layout(j.ID)
	candidates := []string{}
	if j.State.Terminal() {
		for _, artifact := range j.Artifacts {
			if artifact.Name == sidecar.Name && !artifact.Missing && artifact.Path != "" {
				candidates = append(candidates, artifact.Path)
			}
		}
	}
	candidates = append(candidates, sidecar.Path)
	var last error = fs.ErrNotExist
	for _, path := range candidates {
		if err := within(l.Dir, path); err != nil {
			last = err
			continue
		}
		info, err := os.Lstat(path)
		if err != nil {
			last = err
			continue
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			last = fmt.Errorf("job: %s is not a regular file", path)
			continue
		}
		return path, nil
	}
	return "", last
}

// readSidecar reads a transcript the tool is still writing.
func (s *Service) readSidecar(j *Job, source OutputSource, offset int64, max int, chunk *OutputChunk) ([]byte, error) {
	name := strings.TrimPrefix(source.ID, sidecarPrefix)
	var sidecar SidecarLog
	for _, candidate := range j.SidecarLogs {
		if candidate.Name == name {
			sidecar = candidate
		}
	}
	path, err := s.sidecarPath(j, sidecar)
	if errors.Is(err, fs.ErrNotExist) {
		// Declared and not created yet — a tool creates its log when it gets
		// to it. Nothing to read is an answer, not an error.
		chunk.Offset, chunk.Size = 0, 0
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	file, err := openShared(path)
	if err != nil {
		return nil, fmt.Errorf("job: reading %s: %w", filepath.Base(path), err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if offset < 0 {
		if size > int64(max) {
			chunk.Gap = size - int64(max)
		}
		offset = max64(0, size-int64(max))
	}
	if offset > size {
		// The file is shorter than where the reader was: the tool rewrote it
		// (a second run in the same workspace, a log rotated). Start again,
		// and say so.
		chunk.Reset = true
		offset = 0
	}
	chunk.Offset, chunk.Size = offset, size
	length := min64(size-offset, int64(max))
	if length <= 0 {
		return nil, nil
	}
	data := make([]byte, length)
	n, err := file.ReadAt(data, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return data[:n], nil
}

// openShared opens a file another process is writing.
//
// Go opens with FILE_SHARE_READ|FILE_SHARE_WRITE on Windows, so a reader here
// never stops the tool from writing; what can still happen is the tool opening
// its log without sharing for a moment, which Windows answers with a sharing
// violation. That is "busy", not "broken", and it is waited out briefly.
func openShared(path string) (*os.File, error) {
	deadline := time.Now().Add(fsshare.Patience)
	for {
		file, err := os.Open(path)
		if err == nil || !fsshare.IsBusy(err) || time.Now().After(deadline) {
			return file, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
