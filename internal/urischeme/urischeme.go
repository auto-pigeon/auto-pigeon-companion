// Package urischeme registers and unregisters this machine's handler for
// `autopigeon://` links, and reports what is registered.
//
// # What a handler is allowed to be
//
// A URL handler is the one place where a string somebody else chose reaches
// this program without a person typing it. So the command that gets registered
// is fixed here, and it is `companion game open <url>`, which has no approval
// flag at all: it checks the link's shape, records it for the Companion's page
// and raises that page, and starts nothing (244F). The person sees what their
// computer still needs, reviews the exact command there, and decides. A handler registered as anything that
// launches on arrival would make a link into an execution primitive, which is
// exactly what a scheme handler must never be.
//
// The URL reaches the process as ONE argv element — `%u` on the XDG side,
// `"%1"` on the Windows side — and no shell is involved on either. It is then
// parsed by [aub.ParseJoinLink], which accepts this scheme and nothing else.
//
// # Three platforms, three mechanisms, and only two this program performs
//
//	linux    an XDG desktop entry plus a mimeapps.list default. Plain files in
//	         the user's own data directory, so this package writes them itself
//	         and removes exactly what it wrote.
//	windows  HKCU\Software\Classes\autopigeon, through reg.exe. Per-user, so no
//	         administrator rights and nothing machine-wide to leave behind. The
//	         installer writes the same keys declaratively with uninsdeletekey,
//	         which is what makes an uninstall remove them.
//	darwin   CFBundleURLTypes in the .app's Info.plist. LaunchServices reads it
//	         from the bundle; there is no supported way to add a scheme handler
//	         for a loose executable, so this package REFUSES rather than
//	         inventing one, and says where the declaration lives.
//
// # What it refuses
//
// A registration naming an executable that is not there. A handler pointing at
// a path that does not exist is worse than no handler: the user clicks a link,
// the desktop reports nothing, and the failure is invisible. So the executable
// is resolved and checked before anything is written.
package urischeme

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/auto-pigeon/auto-pigeon-companion/internal/fsshare"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/aub"
)

// Scheme is the URL scheme this package registers. It is [aub.JoinLinkScheme]
// rather than a second copy of the word: a handler registered for a scheme the
// parser does not accept would be a handler for links this program refuses.
const Scheme = aub.JoinLinkScheme

// JoinCommand is the subcommand a registered handler runs. It shows the game in
// the Companion and starts nothing — see the package comment.
var JoinCommand = []string{"game", "open"}

// DesktopEntryName is the XDG desktop entry this package writes. It is separate
// from the application's own launcher entry, which packages install into
// /usr/share/applications: this one is NoDisplay, exists only to declare the
// scheme, and can therefore be added and removed without touching a file a
// package manager owns.
const DesktopEntryName = "auto-pigeon-companion-join.desktop"

// MIMEType is how a URL scheme is spelled in XDG's MIME vocabulary.
const MIMEType = "x-scheme-handler/" + Scheme

// WindowsKey is the per-user registry key holding the handler.
const WindowsKey = `HKCU\Software\Classes\` + Scheme

// Methods, reported in [State.Method] so a caller can say what it is looking at
// without switching on the platform again.
const (
	MethodXDG      = "xdg-desktop-entry"
	MethodRegistry = "windows-registry"
	MethodBundle   = "macos-bundle"
)

// ErrNotPerformable reports a platform where this program does not perform the
// registration itself. It is not a failure to register: it means the
// declaration belongs somewhere else, and [State.Detail] says where.
var ErrNotPerformable = errors.New("urischeme: this platform's handler is declared by the application bundle")

// ErrNoExecutable reports that the binary a handler would point at is not
// there.
var ErrNoExecutable = errors.New("urischeme: the executable a handler would point at is not there")

// State is what is registered on this machine, or what would be.
type State struct {
	Scheme   string `json:"scheme"`
	Platform string `json:"platform"`
	Method   string `json:"method"`
	// Registered is whether a handler for this scheme, pointing at this
	// program, is in place now.
	Registered bool `json:"registered"`
	// Command is the argv a handler runs, with the placeholder the platform
	// substitutes the URL into as its own element.
	Command []string `json:"command,omitempty"`
	// Locations are the files or registry keys involved, in the order they are
	// written.
	Locations []string `json:"locations,omitempty"`
	// Detail is one sentence for a person: what happened, or why nothing did.
	Detail string `json:"detail,omitempty"`
}

// Registrar performs the registration. Every field has a working default; they
// exist so a test can exercise every platform on one machine without touching
// the developer's own desktop.
type Registrar struct {
	// GOOS is the platform to act for. Empty means this one.
	GOOS string
	// Executable is the companion binary a handler points at. Empty means the
	// running program.
	Executable string
	// DataHome is XDG_DATA_HOME. Empty means the environment's, then
	// ~/.local/share.
	DataHome string
	// BundlePath is the macOS .app to inspect. Empty means the bundle the
	// running executable is inside, if it is inside one.
	BundlePath string
	// Run executes an external command — reg.exe, and nothing else. Injected so
	// the Windows path is testable on any machine.
	Run func(name string, args ...string) ([]byte, error)
	// Stat reports whether a path exists. Injected for the same reason.
	Stat func(string) (os.FileInfo, error)
}

func (r *Registrar) goos() string {
	if r.GOOS != "" {
		return r.GOOS
	}
	return runtime.GOOS
}

func (r *Registrar) stat(path string) (os.FileInfo, error) {
	if r.Stat != nil {
		return r.Stat(path)
	}
	return os.Stat(path)
}

func (r *Registrar) run(name string, args ...string) ([]byte, error) {
	if r.Run != nil {
		return r.Run(name, args...)
	}
	return exec.Command(name, args...).CombinedOutput()
}

// executable resolves and checks the binary a handler would name.
func (r *Registrar) executable() (string, error) {
	path := strings.TrimSpace(r.Executable)
	if path == "" {
		found, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("urischeme: finding this program: %w", err)
		}
		path = found
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("urischeme: resolving %s: %w", path, err)
	}
	if _, err := r.stat(absolute); err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoExecutable, absolute)
	}
	return absolute, nil
}

// dataHome is where XDG puts per-user application data.
func (r *Registrar) dataHome() (string, error) {
	if r.DataHome != "" {
		return r.DataHome, nil
	}
	if fromEnv := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); fromEnv != "" {
		return fromEnv, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("urischeme: locating the home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share"), nil
}

// Command is the argv a handler runs on this platform, with the URL
// placeholder as its own final element.
func (r *Registrar) Command(executable string) []string {
	argv := append([]string{executable}, JoinCommand...)
	if r.goos() == "windows" {
		return append(argv, "%1")
	}
	return append(argv, "%u")
}

// Status reports what is registered now. It writes nothing.
func (r *Registrar) Status() (State, error) {
	switch r.goos() {
	case "windows":
		return r.windowsStatus()
	case "darwin":
		return r.darwinStatus()
	default:
		return r.xdgStatus()
	}
}

// Register puts the handler in place. It is idempotent: registering twice
// leaves the same files and keys.
func (r *Registrar) Register() (State, error) {
	switch r.goos() {
	case "windows":
		return r.windowsRegister()
	case "darwin":
		state, _ := r.darwinStatus()
		state.Detail = "macOS reads the handler from the .app bundle's Info.plist " +
			"(CFBundleURLTypes). Install the bundle; there is nothing to register for a loose binary."
		return state, ErrNotPerformable
	default:
		return r.xdgRegister()
	}
}

// Unregister removes exactly what Register wrote, and nothing else.
func (r *Registrar) Unregister() (State, error) {
	switch r.goos() {
	case "windows":
		return r.windowsUnregister()
	case "darwin":
		state, _ := r.darwinStatus()
		state.Detail = "macOS reads the handler from the .app bundle's Info.plist. " +
			"Removing the bundle removes the handler; nothing else declares it."
		return state, ErrNotPerformable
	default:
		return r.xdgUnregister()
	}
}

// --- XDG ------------------------------------------------------------------

func (r *Registrar) xdgPaths() (entry, mimeapps string, err error) {
	home, err := r.dataHome()
	if err != nil {
		return "", "", err
	}
	applications := filepath.Join(home, "applications")
	return filepath.Join(applications, DesktopEntryName),
		filepath.Join(applications, "mimeapps.list"), nil
}

func (r *Registrar) xdgStatus() (State, error) {
	entry, mimeapps, err := r.xdgPaths()
	if err != nil {
		return State{}, err
	}
	state := State{Scheme: Scheme, Platform: r.goos(), Method: MethodXDG,
		Locations: []string{entry, mimeapps}}

	body, err := os.ReadFile(entry)
	if err != nil {
		state.Detail = "no desktop entry declares " + MIMEType + " for this program"
		return state, nil
	}
	if !strings.Contains(string(body), MIMEType) {
		state.Detail = entry + " exists but does not declare " + MIMEType
		return state, nil
	}
	defaults, _ := readMIMEApps(mimeapps)
	if defaults[MIMEType] != DesktopEntryName {
		state.Detail = "the desktop entry is there, but " + MIMEType +
			" is not defaulted to it in " + mimeapps
		return state, nil
	}
	state.Registered = true
	state.Command = execLine(string(body))
	state.Detail = MIMEType + " is handled by " + entry
	return state, nil
}

func (r *Registrar) xdgRegister() (State, error) {
	executable, err := r.executable()
	if err != nil {
		return State{}, err
	}
	entry, mimeapps, err := r.xdgPaths()
	if err != nil {
		return State{}, err
	}
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		return State{}, fmt.Errorf("urischeme: creating %s: %w", filepath.Dir(entry), err)
	}
	command := r.Command(executable)
	if err := os.WriteFile(entry, []byte(desktopEntry(command)), 0o644); err != nil {
		return State{}, fmt.Errorf("urischeme: writing %s: %w", entry, err)
	}
	if err := updateMIMEApps(mimeapps, DesktopEntryName); err != nil {
		return State{}, err
	}
	// Desktop environments cache the applications directory. Refreshing it is
	// best effort: the registration is the two files, and a machine with no
	// xdg-utils still has them.
	_, _ = r.run("update-desktop-database", filepath.Dir(entry))

	return State{Scheme: Scheme, Platform: r.goos(), Method: MethodXDG, Registered: true,
		Command:   command,
		Locations: []string{entry, mimeapps},
		Detail:    MIMEType + " now opens " + strings.Join(command, " "),
	}, nil
}

func (r *Registrar) xdgUnregister() (State, error) {
	entry, mimeapps, err := r.xdgPaths()
	if err != nil {
		return State{}, err
	}
	if err := os.Remove(entry); err != nil && !errors.Is(err, os.ErrNotExist) {
		return State{}, fmt.Errorf("urischeme: removing %s: %w", entry, err)
	}
	if err := updateMIMEApps(mimeapps, ""); err != nil {
		return State{}, err
	}
	_, _ = r.run("update-desktop-database", filepath.Dir(entry))

	return State{Scheme: Scheme, Platform: r.goos(), Method: MethodXDG, Registered: false,
		Locations: []string{entry, mimeapps},
		Detail:    MIMEType + " is no longer handled by this program",
	}, nil
}

// desktopEntry renders the .desktop file.
//
// NoDisplay, because the application already has a launcher entry that packages
// install; a second visible one would be a second Auto-Pigeon Companion in the
// menu. No terminal, because `game open` hands the link to the Companion's page,
// which is where the person reads the review.
func desktopEntry(command []string) string {
	var b strings.Builder
	b.WriteString("[Desktop Entry]\n")
	b.WriteString("Type=Application\n")
	b.WriteString("Name=Auto-Pigeon Companion join link\n")
	b.WriteString("Comment=Show an " + Scheme + ":// join link in the Companion, starting nothing\n")
	b.WriteString("Exec=" + desktopExec(command) + "\n")
	b.WriteString("Terminal=false\n")
	b.WriteString("NoDisplay=true\n")
	b.WriteString("MimeType=" + MIMEType + ";\n")
	return b.String()
}

// desktopExec quotes an argv the way the Desktop Entry specification does.
//
// The `%u` field code must stay unquoted, or the desktop passes the literal two
// characters instead of the URL. Everything else is quoted whenever it contains
// anything a reserved character, which is what makes an installation path with
// a space work.
func desktopExec(command []string) string {
	parts := make([]string, 0, len(command))
	for _, part := range command {
		if part == "%u" || part == "%U" {
			parts = append(parts, part)
			continue
		}
		if strings.ContainsAny(part, " \t\n\"'\\><~|&;$*?#()`") {
			replacer := strings.NewReplacer(`\`, `\\\\`, `"`, `\"`, "`", "\\`", "$", "\\$")
			parts = append(parts, `"`+replacer.Replace(part)+`"`)
			continue
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

// execLine pulls the Exec= value out of a desktop entry, as argv.
func execLine(body string) []string {
	for _, line := range strings.Split(body, "\n") {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), "Exec="); found {
			return strings.Fields(value)
		}
	}
	return nil
}

// readMIMEApps reads the [Default Applications] section.
func readMIMEApps(path string) (map[string]string, error) {
	defaults := map[string]string{}
	file, err := os.Open(path)
	if err != nil {
		return defaults, err
	}
	defer file.Close()

	inSection := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inSection = line == "[Default Applications]"
			continue
		}
		if !inSection || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, found := strings.Cut(line, "="); found {
			defaults[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return defaults, scanner.Err()
}

// updateMIMEApps sets or clears this scheme's default, leaving every other
// association and every other section alone.
//
// The file belongs to the user's desktop and holds their choices about every
// other file type. Rewriting it from scratch, or removing a section this
// program did not write, would be this program taking a decision about
// somebody's PDF viewer.
func updateMIMEApps(path, entry string) error {
	existing := map[string]string{}
	other := map[string][]string{}
	var order []string

	if file, err := os.Open(path); err == nil {
		section := ""
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "[") {
				section = trimmed
				if _, seen := other[section]; !seen {
					order = append(order, section)
					other[section] = nil
				}
				continue
			}
			if section == "[Default Applications]" {
				if key, value, found := strings.Cut(trimmed, "="); found {
					existing[strings.TrimSpace(key)] = strings.TrimSpace(value)
					continue
				}
			}
			if section != "" && trimmed != "" {
				other[section] = append(other[section], line)
			}
		}
		file.Close()
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("urischeme: reading %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("urischeme: reading %s: %w", path, err)
	}

	if entry == "" {
		delete(existing, MIMEType)
	} else {
		existing[MIMEType] = entry
	}
	if _, seen := other["[Default Applications]"]; !seen {
		order = append(order, "[Default Applications]")
	}

	var b strings.Builder
	for _, section := range order {
		b.WriteString(section + "\n")
		if section == "[Default Applications]" {
			keys := make([]string, 0, len(existing))
			for key := range existing {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				b.WriteString(key + "=" + existing[key] + "\n")
			}
		}
		for _, line := range other[section] {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("urischeme: creating %s: %w", filepath.Dir(path), err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "mimeapps-*.list")
	if err != nil {
		return fmt.Errorf("urischeme: creating a temporary file in %s: %w", filepath.Dir(path), err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.WriteString(b.String()); err != nil {
		temporary.Close()
		return fmt.Errorf("urischeme: writing %s: %w", name, err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("urischeme: securing %s: %w", name, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("urischeme: closing %s: %w", name, err)
	}
	if err := fsshare.Replace(name, path); err != nil {
		return fmt.Errorf("urischeme: replacing %s: %w", path, err)
	}
	return nil
}

// --- Windows --------------------------------------------------------------

// windowsCommandKey is where the command line lives.
const windowsCommandKey = WindowsKey + `\shell\open\command`

func (r *Registrar) windowsStatus() (State, error) {
	state := State{Scheme: Scheme, Platform: "windows", Method: MethodRegistry,
		Locations: []string{WindowsKey, windowsCommandKey}}
	out, err := r.run("reg", "query", windowsCommandKey, "/ve")
	if err != nil {
		state.Detail = WindowsKey + " does not exist; nothing handles " + Scheme + ":// for this user"
		return state, nil
	}
	state.Registered = true
	state.Command = strings.Fields(strings.TrimSpace(lastValue(string(out))))
	state.Detail = Scheme + ":// is handled by " + strings.TrimSpace(lastValue(string(out)))
	return state, nil
}

func (r *Registrar) windowsRegister() (State, error) {
	executable, err := r.executable()
	if err != nil {
		return State{}, err
	}
	command := r.Command(executable)
	line := windowsCommandLine(command)

	// HKCU only. A per-user key needs no administrator rights, is removed with
	// the user's profile, and cannot affect anybody else on the machine.
	for _, args := range [][]string{
		{"add", WindowsKey, "/ve", "/d", "URL:Auto-Pigeon Companion join link", "/f"},
		{"add", WindowsKey, "/v", "URL Protocol", "/t", "REG_SZ", "/d", "", "/f"},
		{"add", windowsCommandKey, "/ve", "/d", line, "/f"},
	} {
		if out, err := r.run("reg", args...); err != nil {
			return State{}, fmt.Errorf("urischeme: reg %s: %w: %s",
				strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return State{Scheme: Scheme, Platform: "windows", Method: MethodRegistry, Registered: true,
		Command:   command,
		Locations: []string{WindowsKey, windowsCommandKey},
		Detail:    Scheme + ":// now opens " + line,
	}, nil
}

func (r *Registrar) windowsUnregister() (State, error) {
	// One delete, of the one key this program creates, under HKCU. It takes its
	// own subkeys with it and touches nothing else.
	if out, err := r.run("reg", "delete", WindowsKey, "/f"); err != nil {
		if !strings.Contains(strings.ToLower(string(out)), "unable to find") {
			return State{}, fmt.Errorf("urischeme: reg delete %s: %w: %s",
				WindowsKey, err, strings.TrimSpace(string(out)))
		}
	}
	return State{Scheme: Scheme, Platform: "windows", Method: MethodRegistry, Registered: false,
		Locations: []string{WindowsKey},
		Detail:    WindowsKey + " has been removed",
	}, nil
}

// windowsCommandLine renders the argv as a Windows command line.
//
// The executable is quoted ALWAYS, not only when its path happens to contain a
// space. An unquoted program path is the oldest Windows registry mistake there
// is: `C:\Program Files\...` invites the loader to try `C:\Program.exe` first,
// and whether it does depends on where somebody installed the program rather
// than on anything this code can see. `%1` is quoted for the same reason — the
// URL is somebody else's string, and an unquoted one that contained a space
// would arrive as two arguments.
func windowsCommandLine(command []string) string {
	parts := make([]string, 0, len(command))
	for index, part := range command {
		if index == 0 || strings.ContainsAny(part, ` "%`) {
			parts = append(parts, `"`+strings.ReplaceAll(part, `"`, `\"`)+`"`)
			continue
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

// lastValue pulls the data out of `reg query`'s tabular output.
func lastValue(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if index := strings.Index(line, "REG_SZ"); index >= 0 {
			return strings.TrimSpace(line[index+len("REG_SZ"):])
		}
	}
	return ""
}

// --- macOS ----------------------------------------------------------------

func (r *Registrar) darwinStatus() (State, error) {
	state := State{Scheme: Scheme, Platform: "darwin", Method: MethodBundle}
	bundle := r.BundlePath
	if bundle == "" {
		if executable, err := r.executable(); err == nil {
			bundle = bundleContaining(executable)
		}
	}
	if bundle == "" {
		state.Detail = "this program is not running from a .app bundle, so nothing declares " +
			Scheme + ":// . Install the bundle."
		return state, nil
	}
	plist := filepath.Join(bundle, "Contents", "Info.plist")
	state.Locations = []string{plist}
	body, err := os.ReadFile(plist)
	if err != nil {
		state.Detail = "no Info.plist at " + plist
		return state, nil
	}
	if !strings.Contains(string(body), "CFBundleURLSchemes") ||
		!strings.Contains(string(body), "<string>"+Scheme+"</string>") {
		state.Detail = plist + " does not declare " + Scheme + " in CFBundleURLTypes"
		return state, nil
	}
	state.Registered = true
	state.Command = append(append([]string{filepath.Join(bundle, "Contents", "MacOS", "companion")},
		JoinCommand...), "%u")
	state.Detail = plist + " declares " + Scheme + ":// ; LaunchServices registers it when the bundle is installed"
	return state, nil
}

// bundleContaining is the .app an executable is inside, or "".
func bundleContaining(executable string) string {
	dir := filepath.Dir(executable) // Contents/MacOS
	contents := filepath.Dir(dir)   // Contents
	app := filepath.Dir(contents)   // Something.app
	if filepath.Base(dir) == "MacOS" && filepath.Base(contents) == "Contents" &&
		strings.HasSuffix(app, ".app") {
		return app
	}
	return ""
}
