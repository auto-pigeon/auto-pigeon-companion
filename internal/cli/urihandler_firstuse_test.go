package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/config"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/urischeme"
)

// isolatedRegistrar is a link-handler registrar acting on a temporary XDG data
// directory for a temporary "companion" binary: never this machine's desktop.
func isolatedRegistrar(t *testing.T) *urischeme.Registrar {
	t.Helper()
	base := t.TempDir()
	binary := filepath.Join(base, "bin", "companion")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &urischeme.Registrar{
		GOOS: "linux", Executable: binary, DataHome: filepath.Join(base, "data"),
		Run: func(string, ...string) ([]byte, error) { return nil, nil },
	}
}

// fakeRegistry is a Windows registry with one value: the handler's command.
type fakeRegistry struct {
	command string // "" means the key does not exist
	adds    int
}

func (f *fakeRegistry) run(name string, args ...string) ([]byte, error) {
	switch args[0] {
	case "query":
		if f.command == "" {
			return []byte("ERROR: The system was unable to find the specified registry key or value."), errors.New("exit status 1")
		}
		return []byte("\r\n" + urischeme.WindowsKey + `\shell\open\command` + "\r\n    (Default)    REG_SZ    " + f.command + "\r\n"), nil
	case "add":
		f.adds++
		for i, a := range args {
			if a == "/d" && strings.HasSuffix(args[1], `\command`) {
				f.command = args[i+1]
			}
		}
	case "delete":
		f.command = ""
	}
	return nil, nil
}

func windowsEnv(t *testing.T, exe string, reg *fakeRegistry) *Env {
	t.Helper()
	env, _, _ := testEnv(t)
	env.URIRegistrar = &urischeme.Registrar{
		GOOS: "windows", Executable: exe, Run: reg.run,
		Stat: func(string) (os.FileInfo, error) { return nil, nil },
	}
	return env
}

func loadRecord(t *testing.T, env *Env) *config.URIHandler {
	t.Helper()
	c, err := config.LoadFrom(env.ConfigPath)
	if errors.Is(err, config.ErrNotFound) || os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return c.URIHandler
}

// Absolute on the machine running the test (the registrar resolves the path
// it is given), and with a space, as a Windows Desktop path has.
var (
	thisCompanion  = absolute(filepath.Join(string(filepath.Separator)+"opt", "auto-pigeon-companion 1.214", "companion.exe"))
	olderCompanion = absolute(filepath.Join(string(filepath.Separator)+"opt", "auto-pigeon-companion-1.196", "companion.exe"))
)

func absolute(path string) string {
	resolved, err := filepath.Abs(path)
	if err != nil {
		panic(err)
	}
	return resolved
}

// The beta machine's state: no key, no record. The first application launch
// registers `"<exe>" game open "%1"` and records it, with no config.json there
// before.
func TestFirstUseRegistersTheLinkHandlerAndRecordsIt(t *testing.T) {
	reg := &fakeRegistry{}
	env := windowsEnv(t, thisCompanion, reg)
	if _, err := os.Stat(env.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("config.json should not exist yet: %v", err)
	}

	line := uriFirstUse(env, config.Config{}, time.Date(2026, 10, 6, 19, 0, 0, 0, time.UTC))
	if !strings.Contains(line, "registered at first use") {
		t.Fatalf("log line %q", line)
	}
	want := `"` + thisCompanion + `" game open "%1"`
	if reg.command != want {
		t.Fatalf("handler command %q, want %q (one quoted program with spaces, one quoted URL argument)", reg.command, want)
	}
	record := loadRecord(t, env)
	if record == nil || record.Executable != thisCompanion || record.How != "registered" || record.Method != urischeme.MethodRegistry {
		t.Fatalf("record %+v", record)
	}

	// The second launch does nothing more.
	adds := reg.adds
	settings, _ := config.LoadFrom(env.ConfigPath)
	line = uriFirstUse(env, settings, time.Now())
	if reg.adds != adds {
		t.Fatalf("registered again on the second launch: %q", line)
	}
}

// A person removed the handler after the Companion registered it: not undone.
func TestARemovedHandlerIsNotRegisteredAgain(t *testing.T) {
	reg := &fakeRegistry{}
	env := windowsEnv(t, thisCompanion, reg)
	settings := config.Config{URIHandler: &config.URIHandler{Executable: thisCompanion, At: time.Now().Add(-time.Hour)}}
	line := uriFirstUse(env, settings, time.Now())
	if reg.adds != 0 || !strings.Contains(line, "not registered again") {
		t.Fatalf("adds %d, line %q", reg.adds, line)
	}
}

// A new version unpacked into its own folder: the handler still runs the
// Companion that registered it, so this one takes it over.
func TestANewerCompanionTakesOverTheHandlerItsPredecessorRegistered(t *testing.T) {
	reg := &fakeRegistry{command: `"` + olderCompanion + `" game open "%1"`}
	env := windowsEnv(t, thisCompanion, reg)
	settings := config.Config{URIHandler: &config.URIHandler{Executable: olderCompanion, At: time.Now().Add(-time.Hour)}}
	line := uriFirstUse(env, settings, time.Now())
	if !strings.Contains(reg.command, thisCompanion) || !strings.Contains(line, "taken over") {
		t.Fatalf("command %q, line %q", reg.command, line)
	}
	if record := loadRecord(t, env); record == nil || record.Executable != thisCompanion || record.How != "taken over" {
		t.Fatalf("record %+v", record)
	}
}

// Somebody pointed the scheme at another program: left alone.
func TestAHandlerSomebodyElseChoseIsLeftAlone(t *testing.T) {
	other := `C:\Tools\something-else.exe`
	reg := &fakeRegistry{command: `"` + other + `" "%1"`}
	env := windowsEnv(t, thisCompanion, reg)
	settings := config.Config{URIHandler: &config.URIHandler{Executable: olderCompanion, At: time.Now().Add(-time.Hour)}}
	uriFirstUse(env, settings, time.Now())
	if reg.adds != 0 || !strings.Contains(reg.command, other) {
		t.Fatalf("adds %d command %q", reg.adds, reg.command)
	}
}

// Already this Companion (registered from Settings before this feature): only
// the record is written.
func TestAHandlerAlreadyPointingHereIsRecordedNotRewritten(t *testing.T) {
	reg := &fakeRegistry{command: `"` + thisCompanion + `" game open "%1"`}
	env := windowsEnv(t, thisCompanion, reg)
	uriFirstUse(env, config.Config{}, time.Now())
	if reg.adds != 0 {
		t.Fatalf("rewrote a handler that already pointed here")
	}
	if record := loadRecord(t, env); record == nil || record.How != "found" {
		t.Fatalf("record %+v", record)
	}
}

// Linux: the same rule writes the user's own desktop entry.
func TestFirstUseOnLinuxWritesTheDesktopEntry(t *testing.T) {
	env, _, _ := testEnv(t)
	line := uriFirstUse(env, config.Config{}, time.Now())
	entry := filepath.Join(env.URIRegistrar.DataHome, "applications", urischeme.DesktopEntryName)
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("no desktop entry (%v); line %q", err, line)
	}
	if record := loadRecord(t, env); record == nil || record.Method != urischeme.MethodXDG {
		t.Fatalf("record %+v", record)
	}
}

// macOS declares it in the bundle: nothing written, nothing recorded.
func TestFirstUseOnMacOSWritesNothing(t *testing.T) {
	env, _, _ := testEnv(t)
	env.URIRegistrar.GOOS = "darwin"
	line := uriFirstUse(env, config.Config{}, time.Now())
	if !strings.Contains(line, "bundle") || loadRecord(t, env) != nil {
		t.Fatalf("line %q", line)
	}
}

// Paths are compared by the rules of the platform the registrar acts for, not
// of the machine running this test (NEW_310A): Windows without regard to case
// or slash direction, Linux byte for byte.
func TestHandlerPathsAreComparedByTheRegistrarsPlatform(t *testing.T) {
	for _, c := range []struct {
		goos, a, b string
		same       bool
	}{
		{"windows", `C:\Users\Bario\Desktop\new310-dev\companion.exe`, `c:\users\bario\desktop\NEW310-DEV\Companion.EXE`, true},
		{"windows", `C:\Users\bario\companion.exe`, `C:/Users/bario/companion.exe`, true},
		{"windows", `C:\Users\bario\companion.exe`, `C:\Users\bario\other\companion.exe`, false},
		{"linux", "/opt/Companion/companion", "/opt/companion/companion", false},
		{"linux", "/opt/companion//companion", "/opt/companion/companion", true},
		{"linux", "", "/opt/companion/companion", false},
	} {
		if got := samePath(c.goos, c.a, c.b); got != c.same {
			t.Errorf("%s: samePath(%q, %q) = %v, want %v", c.goos, c.a, c.b, got, c.same)
		}
	}
}

// Windows: the record names the same executable in other casing. It is the
// predecessor's own registration of THIS path, so the handler is this
// Companion's and nothing is rewritten or taken over.
func TestAWindowsHandlerInOtherCasingIsThisCompanion(t *testing.T) {
	reg := &fakeRegistry{command: `"` + strings.ToUpper(thisCompanion) + `" game open "%1"`}
	env := windowsEnv(t, thisCompanion, reg)
	settings := config.Config{URIHandler: &config.URIHandler{Executable: strings.ToUpper(thisCompanion), At: time.Now().Add(-time.Hour)}}
	uriFirstUse(env, settings, time.Now())
	if reg.adds != 0 {
		t.Errorf("a handler naming this executable in other casing was rewritten: %q", reg.command)
	}
}

// Linux: the record names this Companion's own path and the handler runs a
// path that differs from it only by case. On Linux that is a different file —
// somebody else's handler — so it stays; case-insensitive comparison would
// have read it as this Companion's earlier registration and taken it over.
func TestALinuxHandlerDifferingOnlyInCaseIsLeftAlone(t *testing.T) {
	env, _, _ := testEnv(t)
	registrar := env.URIRegistrar
	dir, name := filepath.Split(registrar.Executable)
	elsewhere := filepath.Join(dir, strings.ToUpper(name))
	if err := os.WriteFile(elsewhere, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := &urischeme.Registrar{GOOS: "linux", Executable: elsewhere, DataHome: registrar.DataHome, Run: registrar.Run}
	if _, err := other.Register(); err != nil {
		t.Fatal(err)
	}
	settings := config.Config{URIHandler: &config.URIHandler{Executable: registrar.Executable, At: time.Now().Add(-time.Hour)}}
	line := uriFirstUse(env, settings, time.Now())
	state, err := registrar.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.Handler, strings.ToUpper(name)) || strings.Contains(line, "taken over") {
		t.Errorf("a foreign Linux handler was taken over: handler %q, line %q", state.Handler, line)
	}
}
