package job

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

// Bounded capture, and why the bound is on storage rather than on reading.
//
// A compiler that emits a warning per surface can produce hundreds of megabytes
// in a minute. There are two ways to survive that and only one of them is
// right.
//
// The wrong one is to stop reading. The pipe fills, the tool blocks on write,
// and a job that would have finished hangs instead — the supervisor has become
// the reason the build does not complete, and there is nothing in the log to
// say so. That is the "backpressure" that turns into a deadlock.
//
// So this always drains the pipe, at a fixed buffer size, and bounds what it
// *keeps*: the first [headBytes] and the last [tailBytes], with a count of what
// fell out of the middle. Peak memory is a constant regardless of how much the
// program writes, the tool never blocks on the Companion, and the two parts of
// a flood anybody actually reads — how it started and how it ended — are both
// still there.

const (
	// headBytes is how much of the start of a stream is kept. The first
	// screenful of a build is the banner and the version, which is what a user
	// checks first when the wrong tool ran.
	headBytes = 256 << 10
	// tailBytes is how much of the end is kept. The end is where the error is.
	tailBytes = 256 << 10
	// readChunk is the read buffer. Fixed, so memory does not grow with output.
	readChunk = 32 << 10
	// maxLineBytes bounds one line handed to the diagnostic rules. A tool that
	// writes a megabyte with no newline gets it split; a rule matching a
	// substring still matches.
	maxLineBytes = 64 << 10
	// maxDiagnostics bounds the structured findings. A rule that matches every
	// line of a flood would otherwise make the job record itself the flood.
	maxDiagnostics = 500
)

// elision is written into the raw log where bytes were dropped. It is not valid
// output of anything, and it names the count, so a reader can tell a truncated
// log from a short one.
func elision(dropped int64) []byte {
	return []byte(fmt.Sprintf("\n\n[... %d bytes of output were not kept: this log keeps the first %d and the last %d ...]\n\n",
		dropped, headBytes, tailBytes))
}

// capture drains one stream, keeps a bounded copy, and offers each line to a
// callback.
//
// Not safe for concurrent use by design: one capture belongs to one stream and
// one reader goroutine. The counters are read after the goroutine has finished,
// which the executor guarantees with a WaitGroup rather than a mutex.
type capture struct {
	stream string

	head []byte
	tail []byte

	total   int64
	lines   int64
	dropped int64

	// partial is the bytes since the last newline, waiting to become a line.
	partial []byte

	// mirror receives everything, unbounded, as it arrives. It is how the CLI
	// shows a build's output live. Nil for a job nobody is watching.
	mirror io.Writer
	// onLine sees each complete line, without its terminator.
	onLine func(stream string, number int64, line []byte)
}

func newCapture(stream string, mirror io.Writer, onLine func(string, int64, []byte)) *capture {
	return &capture{
		stream: stream,
		head:   make([]byte, 0, 8<<10),
		tail:   make([]byte, 0, 8<<10),
		mirror: mirror,
		onLine: onLine,
	}
}

// drain reads r to EOF. It returns only when the stream is closed, which is
// what makes "the process exited and its output is complete" a thing the
// executor can wait for.
func (c *capture) drain(r io.Reader) {
	buffer := make([]byte, readChunk)
	for {
		n, err := r.Read(buffer)
		if n > 0 {
			c.write(buffer[:n])
		}
		if err != nil {
			// io.EOF and a closed pipe are both "the program is done writing".
			// Nothing else can be usefully done about a read error on a pipe
			// whose other end is a process we are already waiting on.
			c.flushPartial()
			return
		}
	}
}

func (c *capture) write(chunk []byte) {
	c.total += int64(len(chunk))
	if c.mirror != nil {
		// A failed mirror write is not the job's problem: the terminal went
		// away, the log is still being kept, and the process should not be
		// stopped over it.
		_, _ = c.mirror.Write(chunk)
	}
	c.store(chunk)
	c.split(chunk)
}

// store appends to the bounded copy: fill head first, then keep a sliding tail.
func (c *capture) store(chunk []byte) {
	if room := headBytes - len(c.head); room > 0 {
		take := min(room, len(chunk))
		c.head = append(c.head, chunk[:take]...)
		chunk = chunk[take:]
		if len(chunk) == 0 {
			return
		}
	}
	c.tail = append(c.tail, chunk...)
	// Compacted at twice the limit rather than on every append, so the copy is
	// amortised to a constant per byte instead of one per write.
	if len(c.tail) > 2*tailBytes {
		keep := c.tail[len(c.tail)-tailBytes:]
		copy(c.tail, keep)
		c.tail = c.tail[:tailBytes]
	}
}

// split turns the byte stream into lines for the diagnostic rules.
func (c *capture) split(chunk []byte) {
	if c.onLine == nil {
		return
	}
	for len(chunk) > 0 {
		newline := bytes.IndexByte(chunk, '\n')
		if newline < 0 {
			c.partial = append(c.partial, chunk...)
			// A program writing without newlines still gets its output looked
			// at, in maxLineBytes pieces, rather than accumulating without
			// bound in partial.
			for len(c.partial) >= maxLineBytes {
				c.emit(c.partial[:maxLineBytes])
				c.partial = append(c.partial[:0], c.partial[maxLineBytes:]...)
			}
			return
		}
		line := chunk[:newline]
		if len(c.partial) > 0 {
			c.partial = append(c.partial, line...)
			line = c.partial
		}
		c.emit(line)
		c.partial = c.partial[:0]
		chunk = chunk[newline+1:]
	}
}

func (c *capture) emit(line []byte) {
	c.lines++
	c.onLine(c.stream, c.lines, bytes.TrimSuffix(line, []byte("\r")))
}

func (c *capture) flushPartial() {
	if len(c.partial) > 0 {
		c.emit(c.partial)
		c.partial = c.partial[:0]
	}
}

// trim brings the tail down to its exact bound.
//
// The streaming path compacts only at twice the limit, so that dropping the
// front of the tail costs a constant per byte rather than a copy per write.
// That leaves the tail up to twice its size while the program is running —
// still a fixed ceiling, never a function of how much was written — and this
// brings it back to the stated bound once there is nothing more to read.
func (c *capture) trim() {
	if len(c.tail) > tailBytes {
		keep := c.tail[len(c.tail)-tailBytes:]
		copy(c.tail, keep)
		c.tail = c.tail[:tailBytes]
	}
}

// bytesKept is the bounded copy, with an elision marker in the middle when
// anything was dropped.
func (c *capture) bytesKept() []byte {
	c.trim()
	dropped := c.total - int64(len(c.head)) - int64(len(c.tail))
	if dropped <= 0 {
		return append(append([]byte(nil), c.head...), c.tail...)
	}
	out := make([]byte, 0, len(c.head)+len(c.tail)+64)
	out = append(out, c.head...)
	out = append(out, elision(dropped)...)
	out = append(out, c.tail...)
	return out
}

// summary is the StreamLog for the job record.
func (c *capture) summary(file string) StreamLog {
	c.trim()
	dropped := c.total - int64(len(c.head)) - int64(len(c.tail))
	if dropped < 0 {
		dropped = 0
	}
	return StreamLog{
		Bytes:     c.total,
		Stored:    int64(len(c.head)) + int64(len(c.tail)),
		Dropped:   dropped,
		Lines:     c.lines,
		Truncated: dropped > 0,
		File:      file,
	}
}

// diagnosticCollector turns matched lines into records, under a cap, and is
// shared by both streams — so it is the one thing here that needs a lock.
type diagnosticCollector struct {
	mu       sync.Mutex
	found    []Diagnostic
	overflow int
}

func (d *diagnosticCollector) add(record Diagnostic) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.found) >= maxDiagnostics {
		d.overflow++
		return
	}
	d.found = append(d.found, record)
}

func (d *diagnosticCollector) list() []Diagnostic {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := append([]Diagnostic(nil), d.found...)
	if d.overflow > 0 {
		out = append(out, Diagnostic{
			RuleID:   "aucom.diagnostics.truncated",
			Severity: "info",
			Stream:   "both",
			Message:  fmt.Sprintf("%d further matching lines were not recorded; the log still has them.", d.overflow),
		})
	}
	return out
}
