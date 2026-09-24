package job

import (
	"runtime"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/profile"
)

// The environment a job's process gets, and why it is built rather than
// inherited.
//
// A process started with os/exec and no Env inherits the Companion's own, which
// is the user's: their tokens, their proxy credentials, their PATH, their
// LD_PRELOAD. None of that is reviewable, because none of it is in the document
// the user approved — "run qbsp on my map" would quietly also mean "and give it
// my AWS credentials".
//
// So the environment is constructed. Three sources, in this order, each
// narrower than the last:
//
//  1. What the operating system needs to start a process at all. On Windows
//     that is a real list; on the Unix targets it is empty, and a tool that
//     needs PATH is a tool being asked to find another program, which is
//     something a profile declares as an executable instead.
//  2. What this package sets for every job the same way: HOME and the
//     temporary directory pointed inside the job's own workspace, and a fixed
//     locale so a tool's output does not change with the user's settings.
//  3. What the action declared — `environment.inherit` by name, and
//     `environment.set` by value. Both are in the document the user reviewed.
//
// PATH is deliberately absent from all three, and the profile validator refuses
// a document that asks for it: passing PATH through is how "run this exact
// executable" becomes "run whatever is first on a search path this program did
// not choose".

// osRequiredEnv is the platform's own list: names without which a process
// cannot reliably start.
//
// Windows needs SystemRoot to load system DLLs — a process without it fails
// inside the loader, before any of the program's own code runs, with an error
// that says nothing about the environment. The others are what a C runtime and
// a thread pool look for. The Unix targets need nothing: the dynamic loader
// finds libc without help, and every name that could redirect it is refused by
// the profile validator for exactly that reason.
func osRequiredEnv() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	return []string{
		"SystemRoot",
		"SystemDrive",
		"windir",
		"NUMBER_OF_PROCESSORS",
		"PROCESSOR_ARCHITECTURE",
		"PROCESSOR_IDENTIFIER",
		"PATHEXT",
	}
}

// OSRequiredEnv is osRequiredEnv, exported so the documentation and the tests
// read the same list this package uses.
func OSRequiredEnv() []string { return osRequiredEnv() }

// environmentFor builds the full environment for one job as `NAME=value`
// strings, plus the preview entries that go into the record.
//
// declared is the action's resolved environment, which profile.Resolve has
// already reduced to exactly what the document asked for: inherited names it
// found in hostEnv, and set values it rendered.
func environmentFor(l layout, workingDir string, declared map[string]string, action *profile.EnvironmentPolicy, lookup func(string) (string, bool)) ([]string, []EnvEntry) {
	values := map[string]string{}
	sources := map[string]EnvSource{}

	set := func(name, value string, source EnvSource) {
		values[name] = value
		sources[name] = source
	}

	for _, name := range osRequiredEnv() {
		if value, present := lookup(name); present {
			set(name, value, EnvFromExecutor)
		}
	}

	// HOME and TMPDIR point into the job's own directory. A tool that writes a
	// dotfile or a scratch file — and most of them do — writes it somewhere
	// that is cleaned up with the job, instead of into the user's home.
	set("HOME", l.Home, EnvFromExecutor)
	set("USERPROFILE", l.Home, EnvFromExecutor)
	set("TMPDIR", l.Temp, EnvFromExecutor)
	set("TEMP", l.Temp, EnvFromExecutor)
	set("TMP", l.Temp, EnvFromExecutor)
	set("PWD", workingDir, EnvFromExecutor)
	// A fixed locale, so a tool's messages and its number formatting are the
	// same on every machine. Diagnostic rules match literal text; a build that
	// reported "Fehler" on one machine and "error" on another would make them
	// a per-user setting.
	set("LC_ALL", "C", EnvFromExecutor)
	set("LANG", "C", EnvFromExecutor)

	inherited := map[string]bool{}
	if action != nil {
		for _, name := range action.Inherit {
			inherited[name] = true
		}
	}
	for _, name := range sortedKeys(declared) {
		source := EnvFromDocument
		if inherited[name] {
			source = EnvFromHost
		}
		set(name, declared[name], source)
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	environment := make([]string, 0, len(names))
	preview := make([]EnvEntry, 0, len(names))
	for _, name := range names {
		environment = append(environment, name+"="+values[name])
		entry := EnvEntry{Name: name, Source: sources[name]}
		if sources[name] != EnvFromHost {
			// A host-inherited value is named and not recorded. See EnvEntry.
			entry.Value = values[name]
		}
		preview = append(preview, entry)
	}
	return environment, preview
}

// shellQuote renders one argv element the way a shell would show it.
//
// Display only. Nothing re-executes this, and there is no shell whose quoting
// this could be correct for, because the executor never uses one. On Windows
// it follows the command-line convention a Windows program parses its
// arguments with, so a path reads as the path (C:\Users\me, not C:\\Users).
func shellQuote(part string) string {
	if runtime.GOOS == "windows" {
		return windowsQuote(part)
	}
	if part == "" {
		return `""`
	}
	if strings.ContainsAny(part, " \t\n\"'\\$`&|;<>()*?[]{}#~!") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(part) + `"`
	}
	return part
}

// windowsQuote renders one argument exactly as os/exec passes it to a Windows
// program (syscall.EscapeArg, which CommandLineToArgvW reads back): quoted only
// when it holds a space or a tab, a quote escaped with a backslash, and
// backslashes doubled only where they precede a quote or the closing quote.
// A port rather than a call because syscall.EscapeArg exists only on Windows,
// and the display is tested everywhere.
func windowsQuote(part string) string {
	if part == "" {
		return `""`
	}
	needsBackslash, hasSpace := false, false
	for i := 0; i < len(part); i++ {
		switch part[i] {
		case '"', '\\':
			needsBackslash = true
		case ' ', '\t':
			hasSpace = true
		}
	}
	if !needsBackslash && !hasSpace {
		return part
	}
	if !needsBackslash {
		return `"` + part + `"`
	}
	var out strings.Builder
	if hasSpace {
		out.WriteByte('"')
	}
	slashes := 0
	for i := 0; i < len(part); i++ {
		c := part[i]
		switch c {
		default:
			slashes = 0
		case '\\':
			slashes++
		case '"':
			out.WriteString(strings.Repeat(`\`, slashes+1))
			slashes = 0
		}
		out.WriteByte(c)
	}
	if hasSpace {
		out.WriteString(strings.Repeat(`\`, slashes))
		out.WriteByte('"')
	}
	return out.String()
}
