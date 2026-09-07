package enginefixture

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Flag selects the fixture engine inside a host binary.
//
// An argument rather than an environment variable, deliberately: the executor
// passes a job none of its own environment unless the action asked for a name,
// and that is the property several of these tests are checking. A fixture
// selected by an environment variable could not be used to test it.
const Flag = "-aucom-engine-fixture"

// ArgumentSeparator ends the fixture's own flags. Everything after it is the
// engine command line under test, recorded verbatim.
const ArgumentSeparator = "--"

// Behaviour is what the fixture does once it has recorded its arguments.
type Behaviour string

const (
	// BehaviourReady is an engine that starts, prints the line a real engine
	// prints when its console is up, and exits.
	BehaviourReady Behaviour = "ready"
	// BehaviourCrash is an engine that fails the way Quake fails: a Host_Error
	// on stderr and a non-zero status.
	BehaviourCrash Behaviour = "crash"
	// BehaviourStay is an engine that becomes ready and then keeps running,
	// which is what a client and a server both really do. It ends when it is
	// stopped, which is the case the process-tree teardown is about.
	BehaviourStay Behaviour = "stay"
)

// Behaviours is every mode, for an error message that lists them.
var Behaviours = []Behaviour{BehaviourReady, BehaviourCrash, BehaviourStay}

// ReadyLine is the readiness banner. It is a real QuakeSpasm-family line
// because the curated profiles classify it with a diagnostic rule, and a rule
// matched against invented text would be a rule nothing had tested.
const ReadyLine = "Console initialized."

// Record is what the fixture writes down about how it was started.
//
// Argv excludes the fixture's own flags: what a test wants to assert is the
// command line the profile produced, not the scaffolding that captured it.
type Record struct {
	// Argv is the engine command line, one element per argument, exactly as it
	// arrived. A test comparing this to an expected slice is testing that no
	// quoting, splitting or expansion happened anywhere in between.
	Argv []string `json:"argv"`
	// Env is the process environment as `NAME=value`, sorted. What matters
	// about it is usually what is *absent*.
	Env []string `json:"env"`
	// WorkingDir is where the process was started, resolved through symlinks so
	// a comparison against a temporary directory on macOS succeeds.
	WorkingDir string `json:"working_dir"`
	// PID is this process's own id, so a test can tell the tree apart from an
	// unrelated engine started beside it.
	PID int `json:"pid"`
	// Behaviour is what it was told to do, echoed back.
	Behaviour Behaviour `json:"behaviour"`
	// StartedAt is when it recorded itself.
	StartedAt time.Time `json:"started_at"`
}

// options are the fixture's own flags.
type options struct {
	record    string
	behaviour Behaviour
	requires  []string
	linger    time.Duration
	exitCode  int
	children  int
	pidFile   string
}

// Main runs the fixture engine. args are everything after [Flag].
//
// A host test binary dispatches to it from TestMain:
//
//	func TestMain(m *testing.M) {
//		if len(os.Args) > 1 && os.Args[1] == enginefixture.Flag {
//			os.Exit(enginefixture.Main(os.Args[2:]))
//		}
//		os.Exit(m.Run())
//	}
func Main(args []string) int {
	opts, engineArgs, err := parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "engine fixture:", err)
		return 2
	}

	if opts.record != "" {
		if err := writeRecord(opts, engineArgs); err != nil {
			fmt.Fprintln(os.Stderr, "engine fixture:", err)
			return 2
		}
	}
	if opts.pidFile != "" {
		if err := os.WriteFile(opts.pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "engine fixture:", err)
			return 2
		}
	}

	// The data check runs before anything else an engine would do, and the
	// message is the shape Quake's own is: a missing PAK is not a crash, it is
	// a refusal to start.
	for _, required := range opts.requires {
		if _, err := os.Stat(required); err != nil {
			fmt.Fprintf(os.Stderr, "Error: couldn't load %s\n", filepath.ToSlash(required))
			return 1
		}
	}

	for i := 0; i < opts.children; i++ {
		if err := spawnChild(); err != nil {
			fmt.Fprintln(os.Stderr, "engine fixture:", err)
			return 2
		}
	}

	switch opts.behaviour {
	case BehaviourCrash:
		fmt.Println(ReadyLine)
		fmt.Fprintln(os.Stderr, "Host_Error: the fixture engine was asked to crash")
		if opts.exitCode == 0 {
			return 1
		}
		return opts.exitCode
	case BehaviourStay:
		fmt.Println(ReadyLine)
		// Flushed before sleeping: a supervisor watching for readiness has to
		// see the line while the process is still running, which is the whole
		// point of the mode.
		os.Stdout.Sync()
		// A bounded sleep rather than a permanent block. `select {}` would be
		// the natural spelling and the runtime would call it a deadlock and
		// panic; an unbounded one would leave a process behind whenever a test
		// failed before stopping it. Long enough that nothing ends on its own
		// during a run, short enough to be self-clearing.
		time.Sleep(opts.stayFor())
		return opts.exitCode
	default:
		fmt.Println(ReadyLine)
		os.Stdout.Sync()
		if opts.linger > 0 {
			time.Sleep(opts.linger)
		}
		return opts.exitCode
	}
}

// spawnChild starts a grandchild that outlives this process, so a test can
// check that stopping a job takes down the tree and not just its root.
func spawnChild() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	child := exec.Command(self, Flag, "--behaviour", string(BehaviourStay))
	// Deliberately not inheriting this process's stdout: a grandchild holding
	// the output pipe open is a different fault, and the executor has its own
	// test for it.
	child.Stdout, child.Stderr = nil, nil
	if err := child.Start(); err != nil {
		return err
	}
	fmt.Println("fixture child", child.Process.Pid)
	return nil
}

func parse(args []string) (options, []string, error) {
	opts := options{behaviour: BehaviourReady}
	for len(args) > 0 {
		flag := args[0]
		if flag == ArgumentSeparator {
			return opts, append([]string{}, args[1:]...), nil
		}
		value := func() (string, error) {
			if len(args) < 2 {
				return "", fmt.Errorf("%s needs a value", flag)
			}
			return args[1], nil
		}
		switch flag {
		case "--record":
			v, err := value()
			if err != nil {
				return opts, nil, err
			}
			opts.record, args = v, args[2:]
		case "--pid-file":
			v, err := value()
			if err != nil {
				return opts, nil, err
			}
			opts.pidFile, args = v, args[2:]
		case "--behaviour":
			v, err := value()
			if err != nil {
				return opts, nil, err
			}
			if !known(Behaviour(v)) {
				return opts, nil, fmt.Errorf("--behaviour is %q; it is one of: %s", v, behaviourList())
			}
			opts.behaviour, args = Behaviour(v), args[2:]
		case "--require-file":
			v, err := value()
			if err != nil {
				return opts, nil, err
			}
			opts.requires, args = append(opts.requires, v), args[2:]
		case "--linger":
			v, err := value()
			if err != nil {
				return opts, nil, err
			}
			d, parseErr := time.ParseDuration(v)
			if parseErr != nil {
				return opts, nil, fmt.Errorf("--linger is %q: %v", v, parseErr)
			}
			opts.linger, args = d, args[2:]
		case "--exit":
			v, err := value()
			if err != nil {
				return opts, nil, err
			}
			code, parseErr := strconv.Atoi(v)
			if parseErr != nil {
				return opts, nil, fmt.Errorf("--exit is %q, which is not a number", v)
			}
			opts.exitCode, args = code, args[2:]
		case "--children":
			v, err := value()
			if err != nil {
				return opts, nil, err
			}
			count, parseErr := strconv.Atoi(v)
			if parseErr != nil || count < 0 {
				return opts, nil, fmt.Errorf("--children is %q, which is not a count", v)
			}
			opts.children, args = count, args[2:]
		default:
			return opts, nil, fmt.Errorf("unknown fixture flag %q; the engine command line goes after %q", flag, ArgumentSeparator)
		}
	}
	// No separator: there is no engine command line, which is legitimate for
	// the grandchild above.
	return opts, nil, nil
}

// defaultStay is how long BehaviourStay waits to be stopped when --linger says
// nothing.
const defaultStay = 10 * time.Minute

func (o options) stayFor() time.Duration {
	if o.linger > 0 {
		return o.linger
	}
	return defaultStay
}

func known(b Behaviour) bool {
	for _, candidate := range Behaviours {
		if candidate == b {
			return true
		}
	}
	return false
}

func behaviourList() string {
	names := make([]string, 0, len(Behaviours))
	for _, b := range Behaviours {
		names = append(names, string(b))
	}
	return strings.Join(names, ", ")
}

func writeRecord(opts options, engineArgs []string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	environment := append([]string{}, os.Environ()...)
	sort.Strings(environment)
	record := Record{
		Argv:       engineArgs,
		Env:        environment,
		WorkingDir: dir,
		PID:        os.Getpid(),
		Behaviour:  opts.behaviour,
		StartedAt:  time.Now().UTC(),
	}
	if record.Argv == nil {
		record.Argv = []string{}
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if parent := filepath.Dir(opts.record); parent != "." {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(opts.record, append(encoded, '\n'), 0o600)
}

// ReadRecord loads a record the fixture wrote.
func ReadRecord(path string) (Record, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Record{}, fmt.Errorf("enginefixture: reading the record: %w", err)
	}
	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return Record{}, fmt.Errorf("enginefixture: %s is not a fixture record: %w", path, err)
	}
	return record, nil
}

// Lookup returns the value of one recorded environment variable.
func (r Record) Lookup(name string) (string, bool) {
	prefix := name + "="
	for _, entry := range r.Env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix), true
		}
	}
	return "", false
}
