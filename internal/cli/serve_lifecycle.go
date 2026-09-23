package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// What an interactive Companion's terminal shows, and where the rest goes.
//
// A person who double-clicked the program gets two lines and, when it stops,
// one more saying why. Everything a server has always printed — the token
// path, recovery passes, job lines, the readiness warning — is still written,
// to DetailLogName beside config.json, because it is exactly what somebody
// debugging needs and exactly what somebody playing a map does not.
// `companion serve` (server mode) keeps printing it all to stderr, as scripts
// and harnesses expect.

// DetailLogName is the interactive Companion's log file, beside config.json.
// Rewritten at each interactive start, so it describes the run that just
// happened.
const DetailLogName = "companion.log"

// detailLogLimit bounds the log file. A compiler can print hundreds of
// megabytes; the log is the server's own lines, and past this it says so and
// stops rather than filling a disk.
const detailLogLimit = 8 << 20

// noPageHint is when an interactive Companion that opened a browser, and has
// seen no page yet, prints its address once — for the machine where the
// browser command succeeded and no window appeared.
const noPageHint = 20 * time.Second

// boundedLog is an io.Writer that stops at a limit, safe for the several
// goroutines a server logs from.
type boundedLog struct {
	mu      sync.Mutex
	file    *os.File
	written int64
	limit   int64
	full    bool
}

func openDetailLog(configDir string) (*boundedLog, string, error) {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return nil, "", err
	}
	path := filepath.Join(configDir, DetailLogName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, "", err
	}
	return &boundedLog{file: file, limit: detailLogLimit}, path, nil
}

func (l *boundedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.full {
		return len(p), nil
	}
	if l.written+int64(len(p)) > l.limit {
		l.full = true
		fmt.Fprintf(l.file, "[log stopped at %d bytes; later lines were not written]\n", l.limit)
		return len(p), nil
	}
	n, err := l.file.Write(p)
	l.written += int64(n)
	if err != nil {
		// A log that cannot be written must not stop the server.
		l.full = true
	}
	return len(p), nil
}

func (l *boundedLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// timestamped prefixes each line with the wall-clock time, so the log can be
// read against what a person remembers doing.
func timestamped(out io.Writer) func(format string, args ...any) {
	return func(format string, args ...any) {
		fmt.Fprintf(out, "%s "+format+"\n", append([]any{time.Now().Format("15:04:05.000")}, args...)...)
	}
}
