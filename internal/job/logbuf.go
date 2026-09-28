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
// One capture belongs to one stream and one reader goroutine, and the counters
// the job record keeps are read after that goroutine has finished, which the
// executor guarantees with a WaitGroup. The one concurrent reader is the live
// output view (NEW_265): a person watching a build while it runs. It takes
// [capture.mu] and copies at most one bounded chunk, so what it costs the
// program being watched is one uncontended lock per read of the pipe.
type capture struct {
	stream string

	// mu guards head, tail and total against [capture.readFrom]. The line
	// splitter and the counters only its own goroutine touches are outside it.
	mu sync.Mutex

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
	if c.mirror != nil {
		// A failed mirror write is not the job's problem: the terminal went
		// away, the log is still being kept, and the process should not be
		// stopped over it.
		_, _ = c.mirror.Write(chunk)
	}
	c.mu.Lock()
	c.total += int64(len(chunk))
	c.store(chunk)
	c.mu.Unlock()
	c.split(chunk)
}

// readFrom copies what the program has written from an absolute stream offset,
// at most max bytes, while it may still be writing.
//
// Offsets count every byte the program wrote, kept or not, so a reader's cursor
// means the same thing before and after the middle of a flood was dropped: the
// kept head is [0, len(head)), the kept tail is [total-len(tail), total). A
// cursor that points into the dropped middle is moved to the start of the tail
// and the distance is reported as the gap, which is what a reader says instead
// of pretending the stream was continuous.
func (c *capture) readFrom(offset int64, max int) (chunk []byte, start, total, gap int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	total = c.total
	if offset < 0 || offset > total {
		offset = 0
	}
	head := int64(len(c.head))
	if offset < head {
		end := min(head, offset+int64(max))
		return append([]byte(nil), c.head[offset:end]...), offset, total, 0
	}
	tailStart := total - int64(len(c.tail))
	if offset < tailStart {
		gap = tailStart - offset
		offset = tailStart
	}
	from := offset - tailStart
	end := min(int64(len(c.tail)), from+int64(max))
	return append([]byte(nil), c.tail[from:end]...), offset, total, gap
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

// split turns the byte stream into lines: for the diagnostic rules when the
// action declared any, and for the line COUNT either way.
//
// The count is not a by-product of the rules, and used to be. `lineHandler`
// returns nil for an action with no diagnostics, this function returned
// immediately on that nil, and so a job whose profile declared no rules — which
// is most of them, and every one the acceptance harness authors — reported
// `lines: 0` however much the program wrote. A megabyte over ten thousand lines
// was recorded as none of them. That is a job record stating something false
// about what happened, which is worse than a missing field: `bytes` and
// `dropped` were right beside it and right, so nothing looked broken.
//
// Splitting always costs one IndexByte per line and one copy per line that
// straddles a read boundary, which is what any reader of this stream would pay.
// It does not grow with what is kept.
func (c *capture) split(chunk []byte) {
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
	if c.onLine == nil {
		// Counted, and there is no rule to offer it to. The redaction and the
		// rule matching in the handler are the expensive half; a job with no
		// diagnostics pays for neither and still gets a true count.
		return
	}
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
	c.mu.Lock()
	defer c.mu.Unlock()
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
