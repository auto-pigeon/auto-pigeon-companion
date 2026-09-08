package nativeacceptance

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// NoiseOptions is what the kit's own program does.
type NoiseOptions struct {
	// Say prints one line and stops. The approval walk needs a program whose
	// output identifies which version of a document approved it.
	Say string
	// Lines writes this many lines of filler.
	Lines int
	// Both writes to stderr as well, because a log bound is per stream and a
	// flood down one of them says nothing about the other.
	Both bool
	// Seconds keeps the process alive, so a cancellation has something to
	// cancel.
	Seconds float64
	// Exit is the status to end with.
	Exit int
}

// noiseLine is 63 bytes plus a newline, so a caller can compute how many lines
// overrun a byte bound without knowing anything about this function.
const noiseLine = "auto-pigeon companion native acceptance noise 0000000000000000"

// Noise is the acceptance kit's own noisy program.
//
// # Why the product ships one
//
// The approval walk and the log-bound smoke both need a program that is not the
// Companion's own job machinery, is present on a clean machine, and behaves the
// same on every platform. `AUT/AUCOM 219`'s harness wrote a four-line POSIX
// shell stub, and skipped its whole tool-profile lane on Windows for exactly
// that reason: a shell script is not a program on a Windows machine, and a
// batch file is not one on a POSIX machine. This is the portable answer, and it
// costs a few dozen lines in a binary that is already there.
//
// # It does nothing else
//
// No file is written, no directory is read, no network is touched, and nothing
// it prints comes from anywhere but its own arguments. A program a tool profile
// runs under an approval this kit granted must be one whose whole behaviour
// fits in a paragraph.
func Noise(stdout, stderr io.Writer, options NoiseOptions) int {
	if options.Say != "" {
		fmt.Fprintln(stdout, options.Say)
	}
	for index := 0; index < options.Lines; index++ {
		fmt.Fprintf(stdout, "%s %08d\n", noiseLine, index)
		if options.Both {
			fmt.Fprintf(stderr, "%s %08d\n", noiseLine, index)
		}
	}
	if options.Seconds > 0 {
		time.Sleep(time.Duration(options.Seconds * float64(time.Second)))
	}
	return options.Exit
}

// NoiseLineBytes is what one line of filler costs, for a caller sizing a flood.
func NoiseLineBytes() int { return len(fmt.Sprintf("%s %08d\n", noiseLine, 0)) }

// FixtureFiles is the name and content of everything the fixture consists of,
// so a caller writing it to disk cannot get the pairing wrong.
func FixtureFiles() (map[string][]byte, error) {
	wad, err := FixtureWAD()
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		FixtureMapName: []byte(FixtureMap()),
		FixtureWADName: wad,
	}, nil
}

// FixtureSummary is one line describing what the fixture is, for a command that
// wrote it and has to say what it wrote.
func FixtureSummary() string {
	return strings.Join([]string{
		"a sealed room of six brushes",
		"one synthetic WAD2 texture",
		"a player start, a deathmatch start and a light",
	}, ", ")
}
