package urischeme

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrea-dintino/auto-pigeon-companion/internal/aub"
)

// fakeBinary is an executable for the registrar to point at.
func fakeBinary(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func linuxRegistrar(t *testing.T, executable string) (*Registrar, string) {
	t.Helper()
	data := t.TempDir()
	return &Registrar{
		GOOS:       "linux",
		Executable: executable,
		DataHome:   data,
		Run:        func(string, ...string) ([]byte, error) { return nil, nil },
	}, data
}

// The one property a URL handler has to have: the thing it runs must not launch
// anything. `game join` with no --approve resolves the link and prints a plan.
func TestTheRegisteredCommandOnlyEverShowsAPlan(t *testing.T) {
	if want := []string{"game", "join"}; strings.Join(JoinCommand, " ") != strings.Join(want, " ") {
		t.Fatalf("the handler runs %v, want %v", JoinCommand, want)
	}
	registrar := &Registrar{GOOS: "linux"}
	for _, part := range registrar.Command("/usr/bin/companion") {
		if part == "--approve" || part == "--yes" || part == "-y" {
			t.Errorf("the registered command carries %q; a link must never start anything", part)
		}
	}
}

// The scheme registered has to be the scheme the parser accepts, or the handler
// is registered for links this program refuses.
func TestTheSchemeIsTheOneTheParserAccepts(t *testing.T) {
	if Scheme != aub.JoinLinkScheme {
		t.Fatalf("registering %q while the parser accepts %q", Scheme, aub.JoinLinkScheme)
	}
	if _, err := aub.ParseJoinLink(Scheme + "://join/tkt1"); err != nil {
		t.Errorf("a link in the registered scheme is refused by the parser: %v", err)
	}
	if _, err := aub.ParseJoinLink("https://evil.invalid/join/tkt1"); err == nil {
		t.Error("another scheme was accepted")
	}
}

// The URL arrives as one argv element, through a field code, with no shell
// anywhere.
func TestTheURLArrivesAsOneArgumentOnEveryPlatform(t *testing.T) {
	for platform, want := range map[string]string{"linux": "%u", "windows": "%1", "darwin": "%u"} {
		registrar := &Registrar{GOOS: platform}
		command := registrar.Command("/opt/aucom/companion")
		if last := command[len(command)-1]; last != want {
			t.Errorf("%s: the URL placeholder is %q, want %q", platform, last, want)
		}
		for _, part := range command {
			if strings.Contains(part, "sh -c") || part == "cmd" || part == "/bin/sh" {
				t.Errorf("%s: the command goes through a shell: %v", platform, command)
			}
		}
	}
}

func TestRegisteringOnLinuxWritesTheEntryAndTheDefault(t *testing.T) {
	binary := fakeBinary(t, "companion")
	registrar, data := linuxRegistrar(t, binary)

	before, err := registrar.Status()
	if err != nil {
		t.Fatal(err)
	}
	if before.Registered {
		t.Fatal("a fresh machine reported a registered handler")
	}

	state, err := registrar.Register()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Registered {
		t.Error("Register did not report a registration")
	}

	entry := filepath.Join(data, "applications", DesktopEntryName)
	body, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("no desktop entry: %v", err)
	}
	for _, want := range []string{
		"MimeType=" + MIMEType + ";",
		"Exec=" + binary + " game join %u",
		"NoDisplay=true",
		// The plan has to be readable, and a plan printed into no terminal is
		// one nobody sees.
		"Terminal=true",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the desktop entry does not contain %q:\n%s", want, body)
		}
	}

	defaults, err := readMIMEApps(filepath.Join(data, "applications", "mimeapps.list"))
	if err != nil {
		t.Fatal(err)
	}
	if defaults[MIMEType] != DesktopEntryName {
		t.Errorf("%s is defaulted to %q", MIMEType, defaults[MIMEType])
	}

	after, err := registrar.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !after.Registered {
		t.Error("Status does not see what Register wrote")
	}
}

func TestRegisteringTwiceLeavesOneRegistration(t *testing.T) {
	registrar, data := linuxRegistrar(t, fakeBinary(t, "companion"))
	for i := 0; i < 2; i++ {
		if _, err := registrar.Register(); err != nil {
			t.Fatal(err)
		}
	}
	body, err := os.ReadFile(filepath.Join(data, "applications", "mimeapps.list"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(body), MIMEType+"="); got != 1 {
		t.Errorf("%s appears %d times in mimeapps.list:\n%s", MIMEType, got, body)
	}
}

// Unregistering removes exactly what registering wrote. A desktop file holds
// somebody's choices about every other file type, and this program has no
// business changing one of those.
func TestUnregisteringLeavesEverySomebodyElsesAssociationAlone(t *testing.T) {
	registrar, data := linuxRegistrar(t, fakeBinary(t, "companion"))
	applications := filepath.Join(data, "applications")
	if err := os.MkdirAll(applications, 0o755); err != nil {
		t.Fatal(err)
	}
	mimeapps := filepath.Join(applications, "mimeapps.list")
	original := "[Default Applications]\napplication/pdf=org.gnome.Evince.desktop\n" +
		"x-scheme-handler/https=firefox.desktop\n\n[Added Associations]\ntext/plain=gedit.desktop;\n"
	if err := os.WriteFile(mimeapps, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := registrar.Register(); err != nil {
		t.Fatal(err)
	}
	state, err := registrar.Unregister()
	if err != nil {
		t.Fatal(err)
	}
	if state.Registered {
		t.Error("Unregister reported a registration")
	}
	if _, err := os.Stat(filepath.Join(applications, DesktopEntryName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the desktop entry survived: %v", err)
	}

	defaults, err := readMIMEApps(mimeapps)
	if err != nil {
		t.Fatal(err)
	}
	if _, still := defaults[MIMEType]; still {
		t.Error("the scheme is still defaulted to this program")
	}
	for key, want := range map[string]string{
		"application/pdf":        "org.gnome.Evince.desktop",
		"x-scheme-handler/https": "firefox.desktop",
	} {
		if defaults[key] != want {
			t.Errorf("%s is now %q, want %q — an unrelated association was changed", key, defaults[key], want)
		}
	}
	body, err := os.ReadFile(mimeapps)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[Added Associations]", "text/plain=gedit.desktop;"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("an unrelated section was lost: %q is gone:\n%s", want, body)
		}
	}
}

// A handler pointing at a path that is not there is worse than none: the user
// clicks a link and nothing at all happens.
func TestAHandlerIsNeverPointedAtAnExecutableThatIsNotThere(t *testing.T) {
	registrar := &Registrar{
		GOOS:       "linux",
		Executable: filepath.Join(t.TempDir(), "not-installed"),
		DataHome:   t.TempDir(),
	}
	if _, err := registrar.Register(); !errors.Is(err, ErrNoExecutable) {
		t.Fatalf("Register returned %v, want ErrNoExecutable", err)
	}
}

// An installation path with a space has to survive into one argv element.
func TestAnInstallationPathWithASpaceStaysOneArgument(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Auto Pigeon Companion")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "companion")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	registrar := &Registrar{GOOS: "linux", Executable: binary, DataHome: t.TempDir(),
		Run: func(string, ...string) ([]byte, error) { return nil, nil }}
	if _, err := registrar.Register(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(registrar.DataHome, "applications", DesktopEntryName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `Exec="`+binary+`" game join %u`) {
		t.Errorf("the executable was not quoted:\n%s", body)
	}
	// The field code must stay unquoted, or the desktop passes two literal
	// characters instead of a URL.
	if strings.Contains(string(body), `"%u"`) {
		t.Errorf("the field code was quoted:\n%s", body)
	}
}

// --- Windows --------------------------------------------------------------

type recordingRunner struct {
	calls [][]string
	reply map[string]string
	fail  bool
}

func (r *recordingRunner) run(name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	key := strings.Join(args, " ")
	if reply, ok := r.reply[key]; ok {
		return []byte(reply), nil
	}
	if r.fail {
		return []byte("ERROR: The system was unable to find the specified registry key or value."),
			errors.New("exit status 1")
	}
	return nil, nil
}

func TestWindowsRegistrationIsPerUserAndQuotesBothTheBinaryAndTheURL(t *testing.T) {
	runner := &recordingRunner{}
	registrar := &Registrar{GOOS: "windows", Executable: fakeBinary(t, "companion.exe"),
		Run: runner.run}

	state, err := registrar.Register()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Registered {
		t.Error("Register did not report a registration")
	}
	var commandLine string
	for _, call := range runner.calls {
		joined := strings.Join(call, " ")
		// HKLM would need administrator rights and would change the machine for
		// everybody on it.
		if strings.Contains(joined, "HKLM") || strings.Contains(joined, "HKEY_LOCAL_MACHINE") {
			t.Errorf("a machine-wide key was written: %s", joined)
		}
		if len(call) > 2 && call[1] == "add" && call[2] == windowsCommandKey {
			commandLine = call[len(call)-2]
		}
	}
	if commandLine == "" {
		t.Fatalf("no command was written; calls were %v", runner.calls)
	}
	if !strings.HasSuffix(commandLine, ` game join "%1"`) {
		t.Errorf("the command line does not end in a quoted %%1: %q", commandLine)
	}
	if !strings.HasPrefix(commandLine, `"`) {
		t.Errorf("the executable is not quoted: %q", commandLine)
	}
}

func TestWindowsUnregisteringDeletesOnlyThisSchemesKey(t *testing.T) {
	runner := &recordingRunner{}
	registrar := &Registrar{GOOS: "windows", Run: runner.run}
	if _, err := registrar.Unregister(); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("unregistering made %d calls, want one: %v", len(runner.calls), runner.calls)
	}
	call := strings.Join(runner.calls[0], " ")
	if !strings.Contains(call, "delete "+WindowsKey+" /f") {
		t.Errorf("unregister ran %q", call)
	}
	if strings.Contains(call, `Software\Classes"`) || strings.HasSuffix(call, `Software\Classes /f`) {
		t.Errorf("unregister would delete the whole class root: %q", call)
	}
}

// A key that is not there is not an error: unregistering something already
// absent is the state the caller asked for.
func TestWindowsUnregisteringSomethingAbsentIsNotAnError(t *testing.T) {
	registrar := &Registrar{GOOS: "windows", Run: (&recordingRunner{fail: true}).run}
	if _, err := registrar.Unregister(); err != nil {
		t.Fatalf("unregistering an absent key: %v", err)
	}
}

func TestWindowsStatusReadsTheCommandBack(t *testing.T) {
	runner := &recordingRunner{reply: map[string]string{
		"query " + windowsCommandKey + " /ve": "\r\n" + windowsCommandKey +
			"\r\n    (Default)    REG_SZ    \"C:\\Program Files\\Auto-Pigeon Companion\\companion.exe\" game join \"%1\"\r\n",
	}}
	state, err := (&Registrar{GOOS: "windows", Run: runner.run}).Status()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Registered {
		t.Fatal("Status did not see the key")
	}
	if !strings.Contains(state.Detail, "game join") {
		t.Errorf("Status reports %q", state.Detail)
	}
}

// --- macOS ----------------------------------------------------------------

// macOS reads the handler out of the bundle. This program does not perform that
// registration and says so, rather than inventing a mechanism.
func TestMacOSRegistrationIsTheBundlesAndIsRefusedHere(t *testing.T) {
	registrar := &Registrar{GOOS: "darwin", Executable: fakeBinary(t, "companion")}
	state, err := registrar.Register()
	if !errors.Is(err, ErrNotPerformable) {
		t.Fatalf("Register returned %v, want ErrNotPerformable", err)
	}
	if !strings.Contains(state.Detail, "Info.plist") {
		t.Errorf("the refusal does not say where the declaration lives: %q", state.Detail)
	}
	if _, err := registrar.Unregister(); !errors.Is(err, ErrNotPerformable) {
		t.Fatalf("Unregister returned %v, want ErrNotPerformable", err)
	}
}

func TestMacOSStatusReadsTheBundlesInfoPlist(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "Auto-Pigeon Companion.app")
	if err := os.MkdirAll(filepath.Join(bundle, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	registrar := &Registrar{GOOS: "darwin", BundlePath: bundle}

	plist := filepath.Join(bundle, "Contents", "Info.plist")
	if err := os.WriteFile(plist, []byte("<plist><dict></dict></plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := registrar.Status()
	if err != nil {
		t.Fatal(err)
	}
	if state.Registered {
		t.Error("a bundle with no CFBundleURLTypes was reported as registered")
	}

	declared := "<plist><dict><key>CFBundleURLTypes</key><array><dict>" +
		"<key>CFBundleURLSchemes</key><array><string>" + Scheme + "</string></array>" +
		"</dict></array></dict></plist>"
	if err := os.WriteFile(plist, []byte(declared), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err = registrar.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Registered {
		t.Errorf("a bundle declaring the scheme was not recognised: %q", state.Detail)
	}
}

// Status writes nothing, on any platform. It is what a person runs to find out.
func TestStatusWritesNothing(t *testing.T) {
	data := t.TempDir()
	registrar := &Registrar{GOOS: "linux", Executable: fakeBinary(t, "companion"), DataHome: data}
	if _, err := registrar.Status(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("Status created %d entries", len(entries))
	}
}
