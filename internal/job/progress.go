package job

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// What a running tool has said about how far it has got (NEW_365).
//
// Only what the program printed. A percentage is shown when the tool wrote one
// — EricW's `[ 37%]` — and belongs to the phase it was written in: VIS reaches
// `[100%]` at the end of "Calculating Base Vis:" and then starts "Calculating
// Full Vis:", which may run quietly for hours, so the phase change clears the
// number rather than leaving a finished-looking 100% over the slow half. A tool
// that prints dots, or nothing, has no percentage here at all, and the view
// shows activity and elapsed time instead. Nothing is extrapolated from silence,
// from the number of dots or from a timer.

// ProgressReading is the last phase and percentage a tool printed.
type ProgressReading struct {
	// Phase is the tool's own section, named as it named it ("Full VIS",
	// "CalcAmbientSounds"); empty before it printed one.
	Phase string `json:"phase,omitempty"`
	// Percent is the last percentage printed in this phase; nil when none was.
	Percent *int `json:"percent,omitempty"`
	// At is when the phase or the percentage last changed.
	At time.Time `json:"at,omitempty"`
}

var (
	// EricW's VIS phases; the two are named the way the prompt and the tool's
	// readers name them, Base VIS and Full VIS.
	visPhase = regexp.MustCompile(`^Calculating (Base|Full) Vis:?\s*$`)
	// EricW's `---- Name ----` and q3map2's `--- Name (n) ---` section lines. A
	// single word only: EricW's banner `---- vis / ericw-tools 2.0 ----` is the
	// program's name, not a phase.
	sectionPhase = regexp.MustCompile(`^-{3,4} ?([A-Za-z][A-Za-z0-9_]*)(?: \(\d+\))? ?-{3,4}$`)
	// EricW's progress bracket, at the start of a line or of a carriage-return
	// fragment.
	percentPrefix = regexp.MustCompile(`^\[\s*(\d{1,3})%\]`)
)

// maxProgressFragment bounds what is held of an unterminated fragment: a
// progress bracket is at its start, so nothing past this is ever needed.
const maxProgressFragment = 256

type progressTracker struct {
	now func() time.Time

	mu      sync.Mutex
	reading ProgressReading
	partial map[string][]byte
}

func newProgressTracker(now func() time.Time) *progressTracker {
	return &progressTracker{now: now, partial: map[string][]byte{}}
}

// feed reads one chunk of one stream. Lines end at a newline or a carriage
// return — a progress counter rewrites its line with `\r` and never sends a
// newline until it is done — and an unterminated fragment is looked at too, so
// a percentage written ahead of a long silence is seen when it is written.
func (p *progressTracker) feed(stream string, chunk []byte) {
	if p == nil || len(chunk) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	held := p.partial[stream]
	for len(chunk) > 0 {
		cut := strings.IndexAny(string(chunk), "\r\n")
		if cut < 0 {
			held = append(held, chunk...)
			if len(held) > maxProgressFragment {
				held = held[:maxProgressFragment]
			}
			break
		}
		segment := append(held, chunk[:cut]...)
		held = nil
		p.lineLocked(string(segment))
		chunk = chunk[cut+1:]
	}
	p.partial[stream] = held
	if len(held) > 0 {
		p.percentLocked(string(held))
	}
}

func (p *progressTracker) lineLocked(line string) {
	text := strings.TrimSpace(line)
	if text == "" {
		return
	}
	if match := visPhase.FindStringSubmatch(text); match != nil {
		p.phaseLocked(match[1] + " VIS")
		return
	}
	if match := sectionPhase.FindStringSubmatch(text); match != nil {
		p.phaseLocked(match[1])
		return
	}
	p.percentLocked(text)
}

func (p *progressTracker) phaseLocked(phase string) {
	if phase == p.reading.Phase && p.reading.Percent == nil {
		return
	}
	p.reading = ProgressReading{Phase: phase, At: p.now()}
}

func (p *progressTracker) percentLocked(text string) {
	match := percentPrefix.FindStringSubmatch(strings.TrimLeft(text, " \t"))
	if match == nil {
		return
	}
	value, err := strconv.Atoi(match[1])
	if err != nil || value < 0 || value > 100 {
		return
	}
	if p.reading.Percent != nil && *p.reading.Percent == value {
		return
	}
	p.reading.Percent = &value
	p.reading.At = p.now()
}

// Reading is the latest phase and percentage, or nil when the tool printed
// neither.
func (p *progressTracker) Reading() *ProgressReading {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reading.Phase == "" && p.reading.Percent == nil {
		return nil
	}
	out := p.reading
	if p.reading.Percent != nil {
		v := *p.reading.Percent
		out.Percent = &v
	}
	return &out
}
